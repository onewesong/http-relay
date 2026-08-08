---
name: write-http-relay-js
description: 编写、修改、审查和调试 http-relay JavaScript 流量改写脚本，并配置默认脚本、路径绑定的 rewrite Profile 或受控外部 HTTP API。用于用户要求按请求或响应内容改写 URL、Header、Body、状态码，创建本地 Mock/短路响应，通过 relay.http.request 调用白名单 API，按 namespace 或 Profile 区分逻辑，排查脚本 Hook 超时或运行错误，以及新增或更新 .js 改写示例时。
---

# 编写 http-relay JavaScript 改写脚本

## 工作流程

1. 先确认需求属于请求改写、响应改写、短路响应，还是三者组合。
2. 读取 `internal/script/engine.go`、`internal/script/bindings.go` 和 `internal/script/httpapi.go` 核对当前绑定；若只需常见模式，参考 `plugins/examples/relay.example.js`。
3. 选择加载方式：
   - 所有未指定 Profile 的请求共用脚本：使用 `--script <file>`。
   - 通过路径选择不同逻辑：在 TOML 的 `[rewrite.profiles.<name>]` 下注册脚本。
4. 编写普通全局函数 `onRequest`、`onResponse`；不要使用 CommonJS、ES Module、Node.js 或浏览器 API。
5. 让脚本只承担流量转换；不要把进程内全局变量当作可靠存储、计数器或鉴权状态。
6. 添加针对行为的 Go 测试；至少运行相关包测试。修改公共行为时运行 `go test ./...`。
7. 向用户给出配置、启动命令和可直接执行的 curl 验证命令。

## Hook API

两个 Hook 都是可选的：

```js
function onRequest(req) {
  // 原地修改 req；返回响应对象可跳过上游。
}

function onResponse(resp, req) {
  // 原地修改返回给客户端的 resp。
}
```

可写请求字段：

- `req.method`：HTTP 方法字符串。
- `req.url`：完整的 `http://` 或 `https://` URL；修改后会重定向上游，但不会重新选择 Profile。
- `req.host`：上游 Host 覆盖值。
- `req.headers`：普通对象，Header 名使用规范化形式，例如 `Content-Type`。
- `req.body`：字符串。

只读请求上下文：

- `req.namespace`：当前 namespace；默认视图为空字符串。
- `req.rewriteProfile`：当前具名 Profile；未指定时为空字符串。
- `req.originalPath`：进入 Relay 时的原始转义路径。

可写响应字段：

- `resp.status`：数字状态码。
- `resp.headers`：普通对象。
- `resp.body`：字符串。

通过赋值增加或覆盖 Header，通过 `delete` 删除 Header。多值 Header 传入 JS 时以 `, ` 连接，写回时成为单值。Relay 会重新计算正文的 `Content-Length`。

可选外部 HTTP API：

```js
var response = relay.http.request({
  url: "https://config.example.com/v1/features",
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ namespace: req.namespace }),
  timeoutMs: 300,
});
```

- `relay.http.enabled` 表示 `[rewrite.http]` 是否启用。
- `relay.http.request()` 是同步 API，返回 `status`、`headers`、`body`、`url`。
- HTTP `4xx/5xx` 正常返回；参数、策略、网络、超时和大小限制错误抛出可捕获异常。
- 目标必须精确匹配 `allowed_origins`；默认禁止私网地址和重定向。
- API 不继承原始请求 Header、Cookie、Authorization 或代理环境变量。
- Hook timeout 覆盖外部调用；脚本应使用 `try/catch` 提供降级行为。

## 常用模式

改写请求：

```js
function onRequest(req) {
  req.headers["X-Relay-Profile"] = req.rewriteProfile || "default";
  delete req.headers["Cookie"];

  if (req.url.indexOf("/api/v1/") >= 0) {
    req.url = req.url.replace("/api/v1/", "/api/v2/");
  }
}
```

返回本地 Mock，并继续让 `onResponse` 后处理：

```js
function onRequest(req) {
  if (req.url.indexOf("/healthz") >= 0) {
    return {
      status: 200,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ok: true, namespace: req.namespace || "default" }),
    };
  }
}

function onResponse(resp, req) {
  resp.headers["X-Proxied-By"] = "http-relay";
}
```

安全地改写 JSON：

```js
function onResponse(resp, req) {
  var contentType = resp.headers["Content-Type"] || "";
  if (contentType.indexOf("json") < 0 || !resp.body) return;

  try {
    var value = JSON.parse(resp.body);
    value.relayProfile = req.rewriteProfile || null;
    resp.body = JSON.stringify(value);
  } catch (error) {
    console.warn("response body is not valid JSON:", error.message);
  }
}
```

## 配置 Profile

使用配置文件目录作为相对脚本路径的基准：

```toml
[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
timeout = "500ms"
reload = "off"

[rewrite.profiles.mock]
script = "./plugins/examples/rewrite.mock.js"
# 省略 timeout/reload 时继承 --script-timeout/--script-reload
```

显式加载配置并验证两种 namespace 形式：

```bash
go run ./cmd/http-relay --config ./config.toml --web
curl "http://127.0.0.1:7080/@openai/https://example.com"
curl "http://127.0.0.1:7080/team-a/@mock/https://example.com/healthz"
```

不要假设程序自动读取当前目录的 `config.toml`；必须使用 `--config` 或 `HTTP_RELAY_CONFIG`。未知 Profile 返回 `404`，不会回退到 `--script`。reverse 模式不解析 Profile 路径。

## 约束与排错

- 保持 Hook 同步且短小；执行超过 timeout 会返回 `500`。
- 不要使用 `fetch`、Promise、`require`、`import`、DOM、文件系统或 Node.js 内置模块；外部调用只能使用配置允许的同步 `relay.http.request()`。
- 外部 API 调用会增加 Relay 请求延迟，应保持低延迟、限制次数并用 `try/catch` 明确降级。
- 不要把原始请求的 Authorization、Cookie 等敏感 Header 自动复制给外部 API。
- 不要修改只读路由上下文；赋值会使 Hook 失败。
- 使用 `console.log/info/warn/error/debug` 输出诊断信息；TUI 模式会静默这些输出。
- 保留 JSON 解析的 `try/catch`，并先检查 `Content-Type` 和空正文。
- 记住启用任一 Hook 后请求或响应正文可能被缓冲；不要用脚本处理不需要改写的大型流式响应。
- 记住脚本热更新编译失败时会保留上一成功版本；检查启动和 reload 日志，不要只看文件内容。
- 区分 Profile 选择与认证：Profile 不保护 Relay 写入端口，Web JWT 也只保护 Web 页面/API。

## 验证清单

- 运行 `node --check <script.js>` 做基础语法检查（环境存在 Node.js 时）。
- 启动 Relay，确认日志出现默认脚本或 `rewrite profile: name=<name>`。
- 使用 curl 验证正常上游、短路、错误输入和目标 namespace。
- 测试 `onRequest` 与 `onResponse` 使用同一个 Profile 上下文。
- 修改 Registry、绑定或并发行为时运行 `go test -race ./internal/script ./internal/relay`。
