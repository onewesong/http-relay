# JavaScript 流式响应 Hook 实施计划

## 背景与目标

当前只要选中了 Rewrite 脚本，`Handler.serveScripted` 就会通过
`io.ReadAll(resp.Body)` 收集整个上游响应，再调用 `onResponse(resp, req)`。这会让
SSE 在生成结束前无法发送给客户端，因而不能为
`/v1/chat/completions` 到 `/v1/responses` 的兼容层保留逐 token 输出。

本计划增加事件级流式 Hook。Relay 负责正确解析和序列化 SSE 帧；JavaScript 只处理
完整事件并返回零个、一个或多个要写出的事件。这样避免把 TCP chunk 边界暴露给脚本，
并允许脚本在同一请求内保存转换状态。

目标：

- 上游 `text/event-stream` 的首个可转发事件到达后立即可写给客户端，不等待整个响应。
- 保持已有 `onRequest`、`onResponse`、Profile、热更新和无脚本直通行为兼容。
- 同一流内所有 Hook 调用使用同一个 goja runtime 和 JavaScript 状态对象；不同请求间绝不共享状态。
- 正确处理 CRLF/LF、跨网络读边界的 SSE 帧、`data` 多行、客户端断连、脚本异常和上游异常。
- 为 Chat Completions → Responses 的流式转换提供足够的 API，但不把 OpenAI 协议硬编码进 Relay。

非目标：

- 不支持 WebSocket、HTTP/2 双向流或流式请求体改写。
- 不让 JavaScript 直接读写 `io.Reader`、网络连接或 `http.ResponseWriter`。
- 不在第一版支持任意二进制 chunk 改写；流式 Hook 仅处理 SSE。
- 不把每个事件的 JavaScript 全局变量作为可靠的跨请求存储。

## JavaScript API

当脚本定义 `onResponseEvent` 时，它声明自己能够消费 SSE 响应。可选的开始与结束
Hook 形成一个完整生命周期：

```js
function onResponseStart(resp, req) {
  // 上游响应头已获得，正文尚未读取。只能在首帧写出前修改 status、headers。
  resp.headers["Content-Type"] = "text/event-stream";
  delete resp.headers["Content-Length"];
  return { sentRole: false };
}

function onResponseEvent(event, state, req) {
  // event: { event, data, id, retry }
  // 返回 null/undefined 表示丢弃；一个对象或对象数组表示输出事件。
  var value = JSON.parse(event.data);
  return { data: JSON.stringify(value) };
}

function onResponseEnd(state, req) {
  // 可返回一个对象或对象数组，例如最后的 [DONE] 标记。
  return { data: "[DONE]" };
}
```

输入 `event` 的字段均为字符串；不存在的 SSE 字段为 `undefined`。多行 `data:` 会按
SSE 规范用 `\n` 合并为 `data`。注释行（`:`）不传给 JavaScript，Relay 直接原样转发。

返回值必须为以下之一：`null`、`undefined`、单个事件对象、事件对象数组。事件对象：

```js
{ event: "optional-name", data: "required payload", id: "optional-id", retry: "optional-ms" }
```

所有字段必须是字符串；Relay 负责 SSE 转义和增加空行分隔符，因此脚本不能注入裸
Header 或任意传输字节。`data` 中的换行会序列化为多个 `data:` 行。

`onResponseStart` 的返回值是本流私有的 `state`，以 goja.Value 保留到
`onResponseEvent` 和 `onResponseEnd`。它可为任何 JS 值；未返回时两个 Hook 收到
`undefined`。状态生命周期仅限一个响应流，结束或错误时一定释放 runtime。

`onResponseEvent` 必须存在才启用流式模式；开始和结束 Hook 可省略。脚本可以同时定义
`onResponse` 与 `onResponseEvent`，但必须在 `onRequest` 中把
`req.streamResponse` 设为布尔值，以便为本次请求选择缓冲或事件级响应路径。只定义
`onResponseEvent` 的旧脚本默认选择流式路径。

## 路由与兼容语义

1. 无脚本，或脚本只有 `onRequest`：保持现有直通流式转发路径。
2. 脚本定义 `onResponse`：保持现有缓冲语义，适用于 JSON/HTML 等完整响应改写。
3. 请求的 `req.streamResponse` 为真且脚本定义 `onResponseEvent`：仅当上游 `Content-Type` 为 `text/event-stream` 时使用
   新路径；`onResponseStart` 在写出 Header 前执行，每个完整 SSE event 调用一次
   `onResponseEvent`，EOF 后调用 `onResponseEnd`。
4. 第 3 类脚本收到非 SSE 响应时，返回 `502` 并说明“streaming response hook requires
   text/event-stream”，避免默默向客户端返回不兼容的协议。
5. 任何 Hook 返回错误、超过限制或上游读取失败发生在响应首字节前时，按现有错误格式
   返回 `500/502`；首字节已经写出后，不能再改 HTTP 状态，记录错误并关闭连接。

流式路径不设置 `Content-Length`，移除 hop-by-hop Header，保留 `Cache-Control: no-cache`
等上游 SSE Header，并在每次成功写出事件后调用 `http.Flusher.Flush()`。如果 writer
不实现 `http.Flusher`，测试环境可继续写入，正式 HTTP server 则视为内部错误。

## Go 内部设计

### Script Engine

在 `internal/script/engine.go` 中：

- `scriptVersion` 增加 `hasRespStart`、`hasRespEvent`、`hasRespEnd`，并在 `compile()`
  验证三者均为函数或未定义。
- 如果 `hasResp` 与 `hasRespEvent` 同时为真，`New`/`Reload` 返回清晰的验证错误。
- 新增 `HasResponseEventHook()`。
- 新增 `BeginResponseStream(ctx, resp, req) (*ResponseStream, error)`；它从 runtime pool
  借出匹配 generation 的 runtime，创建可取消的 hook state，执行 `onResponseStart`，并
  保存 runtime、版本与 JS state。
- `ResponseStream.OnEvent(event)` 和 `ResponseStream.End()` 分别执行 JS Hook 并校验、
  导出返回事件；`Close()` 幂等地清理 deadline/Interrupt、清空 hook state 并归还 runtime。
  `End()` 无论成功或失败都调用 `Close()`。
- 每次开始、事件和结束回调都使用现有 `Engine.timeout` 作为**单次回调**超时；响应流的
  总生命周期由 `r.Context()`（客户端断连/服务关闭）约束，不能用单次 Hook timeout
  错误地截断一个长回答。

新增 Go 值类型，避免 Handler 直接操作 goja：

```go
type SSEEvent struct {
    Event string
    Data  string
    ID    string
    Retry string
}
```

绑定层增加 event 的 JS 映射，以及严格的 `parseSSEEventResult`：限制数组数量、字段
类型和事件正文大小；拒绝 getter 抛错、非对象、缺失 `data`、以及 `\r`/`\n` 出现在
`event`、`id`、`retry` 字段中的返回值。

### Relay Handler

在 `internal/relay/handler.go` 将现有 `serveScripted` 分为两条明确路径：

- `serveBufferedScripted`：复用当前 `obtainResponse`、`onResponse` 与
  `writeScriptedResponse` 行为。
- `serveStreamingScripted`：执行 `onRequest` 后向上游发起请求，不调用 `io.ReadAll`；
  根据 Header 验证 SSE，创建 `ResponseStream`，写 Header/status，逐帧读取、转换、写出和
  flush。

选择流式路径应在执行 `onRequest` 后、发起上游请求前根据 Engine 的已编译版本确定。
短路响应没有上游 SSE body；若脚本使用 `onResponseEvent`，短路响应也必须带
`Content-Type: text/event-stream`，否则按同样的协议错误处理。

实现一个仅内部使用的 SSE reader/writer（建议 `internal/relay/sse.go`）：

- reader 使用 `bufio.Reader.ReadString('\n')`，累积至空行；不得用默认 64 KiB 限制的
  `bufio.Scanner`。
- 用配置的 `max_sse_event_bytes`（默认 1 MiB）限制单个事件和未终止行，防止内存放大。
- 正确处理 `\n` 与 `\r\n`、多行 data、id/retry/event、注释，以及 EOF 时存在但未以空行
  结束的最后一帧。
- writer 对传入 `data` 的每一行输出 `data: ...\n`，再输出空行；不接受 Header 注入。

## 配置与限制

在 `[rewrite]` 中增加全局字段：

```toml
[rewrite]
max_sse_event_bytes = 1048576
max_sse_events_per_response = 100000
```

- 两项大小/数量限制必须为正数；事件大小另有 `16 MiB` 的代码层面最大上限。
- 未设置时使用安全默认值。所有 Profile 使用同一组 Relay 资源上限。
- 现有配置文件仍严格拒绝未知字段，文档明确 `timeout` 是单次事件回调限制。
- 不添加不透明的总时长限制；连接存活由客户端上下文、上游 transport 与部署层超时控制。

## Chat Completions → Responses 流式脚本

现有独立文件继续处理非流式请求。新增流式示例时不复用当前的 `onResponse`，而使用
`onResponseStart/onResponseEvent/onResponseEnd`：

| Responses 事件 | Chat Completions 输出 |
|---|---|
| `response.output_text.delta` | `choices[0].delta.content` |
| 首个文本/工具事件 | 先输出 `delta.role = "assistant"` |
| `response.function_call_arguments.delta` | `delta.tool_calls[n].function.arguments` |
| `response.output_item.done` 的 function_call | 需要时补充 tool call 的 id/name |
| `response.completed` | `finish_reason: stop` 或 `tool_calls`，再由 `onResponseEnd` 输出 `[DONE]` |

脚本必须忽略未知 Responses event，以便上游新增诊断事件时不破坏会话；对于无法解析的
关键 event，应输出一个 OpenAI 风格 SSE error 并结束流，而非继续生成损坏的 JSON。

## 测试计划

### `internal/script`

- Hook 声明校验，以及 `req.streamResponse` 对缓冲/流式路径的选择。
- start/event/end 的调用顺序、state 隔离、同一流状态保留、runtime 归还和热更新世代隔离。
- 空值/单对象/数组返回值与非法字段、数组过大、JS 异常、超时、取消。
- Header/status 仅能在 start 阶段改写；event/end 不暴露 response header 对象。

### `internal/relay`

- 上游分多次写入（包括把一个 SSE event 切成多个 TCP chunk）时，客户端在 EOF 前收到并
  能解析转换后的首个事件。
- LF、CRLF、data 多行、注释、无尾部空行、超过大小/数量限制。
- 首帧前和首帧后的上游/脚本错误、客户端取消、`Flush` 调用、`Content-Length` 被移除。
- 无 Hook、旧 `onResponse` 和新 `onResponseEvent` 三种路径的回归测试。
- 使用 `go test -race ./internal/script ./internal/relay`，最后执行 `go test ./...`。

### 端到端验证

使用一个本地 SSE 上游依次发送 `response.output_text.delta`、函数调用参数 delta 与
`response.completed`。`curl -N` 调用 Relay，验证首个 `data:` 在上游未关闭前到达，事件
顺序、`[DONE]`、usage 和工具调用均符合 Chat Completions 客户端预期。

## 实施顺序

- [x] 添加 SSE reader/writer 及纯 Go 单元测试，先固定解析和序列化语义。
- [x] 扩展 script Engine 的编译校验、`ResponseStream` 生命周期和返回值验证，完成单元测试。
- [x] 重构 Handler 的缓冲/流式分支，处理 Header、错误、取消和 Flush，完成集成测试。
- [x] 增加 TOML 配置校验、CLI/中文配置文档和示例 Profile。
- [x] 新增 Chat → Responses 流式示例脚本，配合本地上游完成端到端验证。
- [x] 运行 race 与全量测试；验证旧 JSON 改写脚本的行为未变。

## 验收标准

- 选用流式 Hook 的 SSE 请求不再执行 `io.ReadAll(resp.Body)`。
- 客户端在上游 EOF 前收到经 JS 转换的 SSE 事件。
- 不同并发响应流的 JS state 完全隔离，流结束后无 runtime 泄漏。
- 旧脚本、未使用脚本的普通代理和所有现有配置保持兼容。
- 非 SSE 上游、无效 Hook 返回、超限事件和中途断连均产生确定且可观测的行为。
