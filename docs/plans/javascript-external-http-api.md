# JavaScript 外部 HTTP API 实施计划

## 背景与目标

当前 `http-relay` 使用 goja 执行同步的 `onRequest(req)` 和
`onResponse(resp, req)` Hook。脚本可以改写流量，但运行环境没有
`fetch`、`XMLHttpRequest`、Node.js 模块或其他网络 API。

本方案在保持同步 Hook 模型的前提下，向脚本提供受控的
`relay.http.request(options)`，用于调用配置、鉴权、特性开关等低延迟外部
HTTP API。

目标：

- 保持现有同步 Hook、runtime pool、热更新和 Profile 选择行为兼容。
- 默认关闭外部访问能力，只有显式配置后脚本才能发起请求。
- 外部请求与 Hook 使用同一个截止时间，Hook 超时必须取消在途 HTTP 请求。
- 使用独立 HTTP Client，不继承 Relay 上游请求的 Cookie、Authorization、代理或连接状态。
- 通过 origin 白名单、DNS/IP 校验、重定向校验和正文限制降低 SSRF 风险。
- 第一版只支持文本和 JSON 类型的同步 HTTP 请求，不实现 Promise 或浏览器
  `fetch` 兼容层。

非目标：

- 不支持异步 Hook、Promise、`async/await` 或 `Promise.all()`。
- 不提供 WebSocket、SSE、文件上传、流式请求或流式响应。
- 不提供通用 Node.js、浏览器、文件系统或进程 API。
- 不把脚本全局变量作为缓存、凭据存储或跨请求可靠状态。
- 第一版不提供按 Profile 单独配置的网络权限，也不提供配置文件中的命名 Secret。

## JavaScript API

始终提供全局只读对象 `relay.http`：

```js
relay.http.enabled;          // boolean
relay.http.request(options); // 同步调用
```

未启用功能时 `relay.http.enabled` 为 `false`，调用 `request()` 抛出 JavaScript
异常。这样脚本可以检测能力，同时避免因为配置变化出现 `ReferenceError`。

### 请求参数

```js
var result = relay.http.request({
  url: "https://config.example.com/v1/features",
  method: "POST",
  headers: {
    "Authorization": "Bearer explicit-token",
    "Content-Type": "application/json",
  },
  body: JSON.stringify({ namespace: req.namespace }),
  timeoutMs: 500,
});
```

字段规则：

- `url` 必填，必须是绝对 `http://` 或 `https://` URL。
- `method` 可选，默认 `GET`；第一版允许 `GET`、`HEAD`、`POST`、`PUT`、
  `PATCH`、`DELETE`，拒绝 `CONNECT`、`TRACE` 和自定义方法。
- `headers` 可选，必须是普通对象；键和值转换为字符串。
- `body` 可选，必须是字符串；第一版不接受 ArrayBuffer、TypedArray 或流。
- `timeoutMs` 可选，必须是正整数；不能超过配置上限和 Hook 剩余时间。
- 每个 Hook 的调用次数受配置限制；`onRequest` 和 `onResponse` 分别计数。

禁止脚本设置或覆盖以下 Header：

```text
Host
Connection
Proxy-Authorization
Proxy-Connection
Transfer-Encoding
Upgrade
Content-Length
Accept-Encoding
```

`Content-Length` 由 Go 根据 `body` 重新计算。外部调用不会自动复制原始 Relay
请求的任何 Header，尤其不会继承 `Authorization`、`Cookie`、客户端 IP 或
namespace 信息。

### 返回值

HTTP 状态码不是异常，包括 `4xx` 和 `5xx`：

```js
{
  status: 200,
  headers: {
    "Content-Type": "application/json"
  },
  body: "{\"enabled\":true}",
  url: "https://config.example.com/v1/features"
}
```

字段规则：

- `status`：最终响应状态码。
- `headers`：普通对象；多值 Header 使用 `, ` 连接，与现有 Hook Header 语义一致。
- `body`：字符串；超过大小限制时整个调用失败，不返回截断正文。
- `url`：最终 URL；关闭重定向时与请求 URL 相同。

第一版定位于文本和 JSON API。对于二进制响应，脚本得到的字符串不保证可逆；
后续如有需要再增加显式 `bodyBase64`，不在第一版隐式转换。

### 错误语义

以下情况由 `relay.http.request()` 抛出可捕获的 JavaScript `Error`：

- 功能未启用。
- 参数类型、URL、方法或 Header 非法。
- origin 未在白名单中。
- DNS 解析结果包含被禁止的 IP。
- 连接、TLS、DNS 或读取响应失败。
- 超时或 Hook 被取消。
- 重定向被禁止或重定向目标校验失败。
- 请求体、响应体或调用次数超过限制。

错误消息应稳定、简短且不包含响应正文、认证 Header、完整凭据或底层连接敏感
信息。第一版不增加结构化错误类型；脚本只应把异常用于降级，不应依赖完整错误
字符串进行业务判断。

推荐脚本写法：

```js
function onRequest(req) {
  if (!relay.http.enabled) return;

  try {
    var response = relay.http.request({
      url: "https://config.example.com/v1/features",
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ namespace: req.namespace }),
      timeoutMs: 300,
    });
    if (response.status !== 200) return;

    var feature = JSON.parse(response.body);
    req.headers["X-Feature-Enabled"] = String(feature.enabled === true);
  } catch (error) {
    console.warn("feature lookup failed:", error.message);
  }
}
```

## TOML 配置

外部 HTTP 权限使用全局配置，默认关闭：

```toml
[rewrite.http]
enabled = true

# 精确匹配 scheme、hostname 和有效端口；第一版不支持通配符。
allowed_origins = [
  "https://config.example.com",
  "https://auth.example.com:8443",
]

timeout = "1s"
max_timeout = "3s"
max_request_body_bytes = 1048576
max_response_body_bytes = 1048576
max_calls_per_hook = 3
follow_redirects = false
allow_private_networks = false
```

配置规则：

- `enabled` 默认 `false`。
- 启用时 `allowed_origins` 必须至少包含一个合法 origin。
- origin 必须是规范的 `http` 或 `https` origin，不得包含用户信息、路径、query
  或 fragment。
- 未显式端口时按 scheme 规范化为 `http:80` 或 `https:443` 后比较。
- hostname 比较不区分大小写；禁止尾随点、空 hostname 和非规范端口。
- 第一版不支持 `*.example.com`、CIDR 或正则表达式，避免产生模糊授权范围。
- `timeout` 是单次外部请求默认超时，必须大于零。
- `max_timeout` 是 JS `timeoutMs` 可请求的上限，必须大于等于 `timeout`。
- 单次有效超时取 `timeoutMs`、`max_timeout` 和 Hook 剩余时间的最小值。
- 请求体和响应体限制必须为正数，并设置合理的编译期上限，防止错误配置导致
  内存放大。
- `max_calls_per_hook` 必须为正数，建议默认 `3`，并设置较小的编译期上限。
- `follow_redirects` 默认 `false`；开启时每一次跳转都重新执行完整 URL、origin、
  DNS 和 IP 校验，并限制最多 3 次跳转。
- `allow_private_networks` 默认 `false`。即使设置为 `true`，目标仍必须命中
  `allowed_origins`。
- TOML 严格模式继续拒绝未知字段。

`--script-timeout` 或 Profile 的 `timeout` 必须覆盖完整 Hook，包括所有外部调用
和后续 JavaScript 执行。例如外部调用最多允许 1 秒时，Profile Hook 超时应配置
为更大的值：

```toml
[rewrite.http]
enabled = true
allowed_origins = ["https://config.example.com"]
timeout = "800ms"
max_timeout = "1s"

[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
timeout = "1500ms"
reload = "off"
```

## 超时与取消模型

这是本功能必须先解决的基础问题。

当前 Hook 超时通过 `goja.Runtime.Interrupt()` 实现。该机制可以打断正在执行的
JavaScript，但不能保证立即中断一个正在 Go 原生函数中阻塞的 HTTP 请求。如果
直接在现有 Binding 中调用 `http.Client.Do()`，脚本超时后网络调用仍可能占用
连接和 runtime。

实施时将一次 Hook 调用改为以下生命周期：

```text
创建 hook context，deadline = Hook timeout
→ 从 pool 获取 runtime
→ 把 hook context 和本次调用计数挂到该 runtime 的临时执行状态
→ 执行 onRequest/onResponse
→ context 到期时同时取消 HTTP 请求并 Interrupt runtime
→ Hook 返回后清空临时状态和 Interrupt
→ runtime 放回 pool
```

建议扩展 `pooledRuntime`：

```go
type hookState struct {
    context   context.Context
    calls     int
    startedAt time.Time
}

type pooledRuntime struct {
    rt    *goja.Runtime
    gen   uint64
    state *hookState
}
```

每个 runtime 在被借出期间只服务一个 Hook，因此 Binding 可以通过闭包读取当前
`hookState`。归还 pool 前必须清空状态，防止 context、调用次数或请求信息泄漏到
下一次 Hook。

Probe runtime 也需要安装 `relay.http`，以允许脚本在顶层引用函数；但 probe 和
脚本初始化阶段没有 Hook context，实际调用 `relay.http.request()` 必须抛出
“outside hook”错误，禁止启动时发起网络请求。

## HTTP Client 与 SSRF 防护

### 独立 Client

脚本外部调用使用独立的 `http.Client` 和 `http.Transport`：

- 不复用 Relay 上游 Client。
- 第一版不读取 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY` 或 `NO_PROXY`。
- 不携带 CookieJar。
- 不自动重试，避免放大请求和产生非幂等副作用。
- 合理限制空闲连接数、每主机连接数和空闲连接寿命。
- 使用 Hook context 控制 DNS、连接、TLS、写入和读取全过程。
- 自动解压缩后再应用响应正文大小限制，避免压缩炸弹绕过限制。

### URL 与 origin 校验

请求前执行：

1. 严格解析绝对 URL。
2. 只接受 `http` 和 `https`。
3. 拒绝 URL userinfo、空 hostname、异常端口和控制字符。
4. 规范化 scheme、IDNA hostname 和有效端口。
5. 与 `allowed_origins` 精确匹配。
6. 解析 DNS，并检查所有候选 IP。
7. 通过受控 `DialContext` 直接连接已检查的 IP，避免校验与连接之间再次解析
   导致 DNS rebinding。

### 默认禁止的地址

`allow_private_networks = false` 时拒绝：

- unspecified、loopback、private、link-local、multicast 和 broadcast 地址。
- IPv4/IPv6 本机与私网范围。
- IPv4-mapped IPv6 对应的被禁 IPv4 地址。
- 云元数据常用 link-local 地址，例如 `169.254.169.254`。

不能只依赖字符串 hostname 或 `net.IP.IsPrivate()`；最终连接的每个 IP 都必须
按完整策略检查。一个 hostname 如果解析出任意被禁止地址，第一版整体拒绝，
不从剩余地址中选择连接，避免 DNS 返回集变化带来的策略歧义。

### 重定向

默认不跟随重定向。开启后：

- 最多允许 3 次。
- 每个 Location 都按新请求重新执行完整校验。
- 跨 origin 跳转不得自动携带 `Authorization`、`Cookie` 等敏感 Header。
- `307/308` 保留方法和正文，其他状态遵循 Go 标准行为，但必须重新检查请求体
  大小和调用 context。
- 任一跳转校验失败时整个调用抛出异常，不向脚本返回中间响应。

## 内部结构设计

### 配置层

在 `internal/config` 增加：

```go
type RewriteConfig struct {
    Profiles map[string]RewriteProfile `toml:"profiles"`
    HTTP     RewriteHTTPConfig         `toml:"http"`
}
```

`RewriteHTTPConfig` 负责 TOML 字段、默认值和静态校验。规范化后的 origin、最大
限制和网络策略应转换为 `internal/script` 使用的运行时配置，避免脚本包依赖 TOML
结构。

### Script HTTP 服务

建议新增 `internal/script/httpapi.go`：

```go
type HTTPOptions struct {
    Enabled              bool
    AllowedOrigins       []string
    DefaultTimeout       time.Duration
    MaxTimeout           time.Duration
    MaxRequestBodyBytes  int64
    MaxResponseBodyBytes int64
    MaxCallsPerHook      int
    FollowRedirects      bool
    AllowPrivateNetworks bool
}

type HTTPService struct {
    // 规范化策略、resolver、dialer、transport 和 client
}
```

`HTTPService` 在进程启动时构建一次，可以被默认 Engine 和所有 Profile Engine
共享。它本身必须并发安全，但每个 Hook 的调用次数和 context 存在对应
`pooledRuntime.hookState` 中。

为便于测试，HTTP 执行依赖应通过窄接口或可替换 resolver/dialer 注入，不直接在
单元测试中依赖公网 DNS。

### Binding 安装

把当前只有 `console` 的初始化整理为统一 Binding 安装过程：

```text
installBindings(runtime, console, httpService, stateAccessor)
├── installConsole
└── installRelayHTTP
```

`installRelayHTTP` 创建只读 `relay` 和 `relay.http` 对象。脚本不能替换
`relay.http.request`、修改 `enabled` 或删除这些属性。

Binding 只负责：

- 从 goja 值读取并验证参数。
- 获取当前 Hook context 和调用计数。
- 调用 `HTTPService.Request`。
- 把 Go 响应转换为普通 JS 对象。
- 把 Go 错误转换成可捕获的 JavaScript `Error`。

URL 安全、DNS、重定向、超时和正文限制全部放在 `HTTPService`，避免 Binding
承担网络策略。

### Engine 与 Registry

在 `script.Options` 和 `ProfileOptions` 中传入共享的 `HTTPService`。默认脚本、
磁盘 Profile 和 `builtin:` Profile 使用相同全局网络权限。

热更新只替换 JavaScript program，不重建 HTTP Client。配置文件本身目前不热
更新，因此网络白名单变化需要重启进程，与现有配置加载行为一致。

## 实施步骤

### 第一阶段：配置与策略模型

- [x] 在 `internal/config` 增加 `[rewrite.http]` 配置结构、默认值和严格校验。
- [x] 实现 origin 规范化，拒绝路径、userinfo、query、fragment、通配符和非法端口。
- [x] 在 `cmd/http-relay` 启动阶段构建共享 `HTTPService`。
- [x] 把服务传入默认 Engine 和所有 Profile Engine。
- [x] 启动日志只输出 enabled、origin 数量、超时和大小限制，不输出完整凭据或脚本
   请求内容。

### 第二阶段：Hook context 与可靠取消

- [x] 为每次 `OnRequest`、`OnResponse` 创建带 Hook deadline 的 context。
- [x] 为 `pooledRuntime` 增加仅在借出期间有效的 `hookState`。
- [x] context 到期时先取消网络操作，再调用 `Runtime.Interrupt()`。
- [x] Hook 结束后停止超时回调、清除 Interrupt、清空 state，再归还 runtime。
- [x] 验证超时后的 runtime 可以继续复用，且不会携带上一请求的 context 或计数。

### 第三阶段：HTTPService

- [x] 实现请求参数和 Header 校验。
- [x] 实现 origin 精确白名单。
- [x] 实现可测试的 DNS 解析、IP 分类和受控 DialContext。
- [x] 构建不使用环境代理、CookieJar 和自动重试的专用 Transport。
- [x] 实现请求体、响应体、超时和调用次数限制。
- [x] 实现默认拒绝重定向及可选的逐跳校验。
- [x] 对外返回稳定的响应结构和脱敏错误。

### 第四阶段：goja Binding

- [x] 安装只读 `relay.http.enabled` 和 `relay.http.request`。
- [x] 支持普通对象参数、同步返回和可捕获异常。
- [x] 禁止初始化阶段调用外部 API。
- [x] 确保返回对象和 Header 对象是每次调用新建，runtime 复用时不泄漏状态。
- [x] 保持没有 `[rewrite.http]` 配置时现有脚本行为不变。

### 第五阶段：文档与示例

- [x] 更新中英文 README 的 Hook API、配置、安全边界和超时说明。
- [x] 更新 `config.example.toml`，保留默认关闭的安全示例。
- [x] 在 `plugins/examples` 增加一个外部配置查询示例，不在内置 OpenAI 脚本中默认
   发起网络请求。
- [x] 更新 `write-http-relay-js` 技能文档，说明 `relay.http.request()` 能力和限制。

## 测试计划

### 配置测试

- 默认关闭，未配置时保持兼容。
- enabled 但白名单为空时启动失败。
- origin 大小写、默认端口和 IDNA 规范化。
- 拒绝通配符、路径、userinfo、query、fragment、未知字段和非法限制。
- Profile 与默认脚本共享同一个 HTTP 策略。

### Binding 测试

- disabled、outside hook、缺失 URL、非法 method、Header 和 timeout 参数。
- `relay`、`relay.http`、`enabled` 和 `request` 为只读属性。
- GET、POST、Header、请求体和响应字段转换正确。
- `4xx/5xx` 正常返回，网络错误和策略错误抛出可捕获异常。
- 多值响应 Header 使用 `, ` 连接。
- 顶层可引用 API，但不能在脚本初始化阶段调用。

### 网络安全测试

- 允许精确 origin，拒绝 scheme、hostname 或端口不匹配。
- 拒绝 loopback、private、link-local、multicast、IPv4-mapped IPv6 和元数据地址。
- `allow_private_networks=true` 只放宽 IP 策略，不绕过 origin 白名单。
- DNS 返回混合公网/私网地址时整体拒绝。
- 校验后连接已解析 IP，不发生第二次不受控 DNS 解析。
- 默认拒绝重定向；开启后逐跳检查，并删除跨 origin 敏感 Header。
- 环境代理变量不会影响脚本 HTTP Client。

### 限制与取消测试

- 请求体和响应体等于限制时成功，超过一个字节时失败。
- 压缩响应按解压后的正文限制。
- `timeoutMs`、配置上限和 Hook 剩余时间取最小值。
- 慢 DNS、慢连接、慢响应头和慢正文读取都能被 context 取消。
- Hook 超时后外部请求结束，runtime 可以继续处理下一请求。
- 超过 `max_calls_per_hook` 时失败；下一 Hook 重新从零计数。

### 并发与回归测试

- 多个并发 Hook 不串用 context、调用计数、响应对象或 Header。
- runtime pool 复用无残留状态。
- 热更新前后的 runtime 都使用同一 HTTP 策略。
- 内置脚本和磁盘脚本行为一致。
- 未启用 HTTP 能力时现有脚本、纯转发和响应流式行为不变。
- 运行：

```bash
go test ./...
go test -race ./internal/script ./internal/relay
```

## 可观测性

第一版只记录必要的诊断信息：

- 启动时记录脚本 HTTP 能力是否启用、允许 origin 数量和限制摘要。
- 调用失败由脚本自行 `catch` 并决定是否通过 `console.warn` 输出。
- Relay 不默认记录外部请求 Header、请求体、响应体或 URL query。
- Go 层错误日志不得输出 Authorization、Cookie 或 URL userinfo。
- 后续如果增加指标，只记录调用次数、耗时、状态码类别和错误类别；hostname
  应按配置决定是否作为标签，避免高基数和敏感信息泄漏。

## 兼容性与失败策略

- 没有配置 `[rewrite.http]` 时，`relay.http.enabled === false`，现有 Hook 不受影响。
- 外部 API 调用失败只会使当前 Hook 抛错；脚本可以通过 `try/catch` 自行降级。
- 未捕获异常沿用现有行为：当前 Relay 请求返回脚本执行错误，不静默继续上游。
- HTTP `4xx/5xx` 不视为执行错误，由脚本决定如何处理。
- 外部调用完成后，`onRequest` 仍可短路响应；短路后 `onResponse` 行为保持不变。
- 功能不会改变 Profile 选择、namespace、Web JWT 或上游代理配置。

## 验收标准

- 脚本能在 `onRequest` 和 `onResponse` 中同步调用白名单内的 HTTP/HTTPS API。
- 默认配置不能访问任何外部地址。
- Hook 超时能可靠取消在途 HTTP 请求，不遗留长期占用的 runtime 或连接。
- 白名单、DNS/IP、重定向、Header 和正文限制均有自动化测试。
- 并发和 runtime pool 复用不产生跨请求状态泄漏。
- `go test ./...` 和相关 race 测试通过。
- README、示例配置、示例脚本和技能文档与实际 API 一致。

## 后续演进

第一版稳定后再评估：

- 按 Profile 配置独立 origin 白名单和调用限制。
- 从环境变量或 Secret Provider 注入命名凭据，避免把 Token 写进 JS。
- 增加显式 Base64 二进制请求/响应字段。
- 增加熔断、限流、指标和短期缓存。
- 在确有并发外部调用需求时，单独设计 Promise/event-loop 方案，不把同步 API
  伪装成浏览器 `fetch`。
