# http-relay 配置详解

本文说明 `http-relay` 当前支持的 TOML 配置、默认值、环境变量覆盖关系和常见
部署注意事项。可复制仓库根目录的
[`config.example.toml`](../config.example.toml) 作为起点。

## 加载配置文件

程序不会自动读取当前目录下的 `config.toml`。配置路径优先级为：

1. 命令行 `--config <path>`
2. 环境变量 `HTTP_RELAY_CONFIG`
3. 未提供配置文件，使用内置默认值

```bash
http-relay --config ./http-relay.toml --web
```

或：

```bash
HTTP_RELAY_CONFIG=./http-relay.toml http-relay --web
```

配置使用严格 TOML 解析：未知字段、错误类型、非法枚举或不满足约束的值都会使
程序在启动阶段失败。配置文件中的相对脚本路径以配置文件所在目录为基准，不以
进程当前工作目录为基准。

## 配置结构总览

```toml
[rewrite.http]
# JavaScript 外部 HTTP API

[rewrite.profiles.<name>]
# 具名 JavaScript Rewrite Profile，可配置多个

[web]
# Web UI 记录设置

[web.auth]
# Web UI JWT 认证

[web.auth.namespaces]
# 各 namespace 的公开/受保护策略
```

## JavaScript 外部 HTTP：`[rewrite.http]`

控制脚本中的同步 `relay.http.request()`。默认关闭。

```toml
[rewrite.http]
enabled = true
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

| 字段 | 默认值 | 说明 |
|---|---:|---|
| `enabled` | `false` | 是否开放 `relay.http.request()`。关闭时调用会抛出可捕获异常。 |
| `allowed_origins` | `[]` | 允许访问的 origin 精确白名单；启用时不能为空。 |
| `timeout` | `1s` | 单次外部请求的默认超时。 |
| `max_timeout` | `3s` | 脚本通过 `timeoutMs` 请求的最大超时，不能小于 `timeout`。 |
| `max_request_body_bytes` | `1048576` | 单次请求体上限，最大可配置为 `16777216`（16 MiB）。 |
| `max_response_body_bytes` | `1048576` | 解压后的响应体上限，最大可配置为 `16777216`（16 MiB）。 |
| `max_calls_per_hook` | `3` | 每次 `onRequest` 或 `onResponse` 最多调用次数，最大为 `16`。两个 Hook 分别计数。 |
| `follow_redirects` | `false` | 是否允许重定向；开启后仍会限制跳转次数，并逐跳重新校验。 |
| `allow_private_networks` | `false` | 是否允许私网、loopback 等地址；开启后仍必须命中 origin 白名单。 |

### `allowed_origins` 规则

origin 由 scheme、hostname 和有效端口组成：

```text
https://config.example.com      -> https://config.example.com:443
http://service.example.com      -> http://service.example.com:80
https://service.example.com:8443
```

规则：

- 只允许 `http` 和 `https`。
- 不允许用户名密码、路径、query、fragment、尾随点或通配符。
- hostname 不区分大小写，并规范化国际化域名。
- 第一版不支持 `*.example.com`、CIDR 或正则表达式。
- 默认拒绝 unspecified、loopback、私网、link-local、multicast、CGNAT 和云元数据
  地址。
- DNS 解析和实际连接使用同一批已校验 IP，降低 DNS rebinding 风险。
- 外部调用使用独立 HTTP Client，不读取 `HTTP_PROXY`、`HTTPS_PROXY`、
  `ALL_PROXY` 或 `NO_PROXY`，也不自动继承原始请求的 Cookie 和 Authorization。

### 与 Hook timeout 的关系

Profile 的 `timeout` 或默认脚本的 `--script-timeout` 覆盖整个 Hook，包括外部
请求和后续 JavaScript 执行。有效外部请求时间不会超过：

```text
JS timeoutMs、rewrite.http.max_timeout、Hook 剩余时间三者中的最小值
```

如果外部请求允许 1 秒，Hook timeout 应留出额外处理时间：

```toml
[rewrite.http]
enabled = true
allowed_origins = ["https://config.example.com"]
timeout = "800ms"
max_timeout = "1s"

[rewrite.profiles.external-config]
script = "./plugins/examples/rewrite.external-config.js"
timeout = "1500ms"
reload = "watch"
```

## 流式 Rewrite 限制：`[rewrite]`

定义事件级 SSE 响应 Hook 的资源上限：

```toml
[rewrite]
max_sse_event_bytes = 1048576
max_sse_events_per_response = 100000
```

- `max_sse_event_bytes`：一个上游 SSE 帧可占用的最大字节数，默认 `1048576`，最大 `16777216`。
- `max_sse_events_per_response`：单个上游响应允许处理的最大 SSE 帧数，默认 `100000`。

这些限制只影响定义了 `onResponseEvent` 的脚本。普通代理和传统 `onResponse` 脚本不受影响。

## Rewrite Profile：`[rewrite.profiles.<name>]`

Profile 根据 Relay 请求路径选择不同 JavaScript 脚本：

```toml
[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
timeout = "500ms"
reload = "off"

[rewrite.profiles.mock]
script = "./plugins/examples/rewrite.mock.js"
timeout = "300ms"
reload = "watch"

[rewrite.profiles.openai-compat]
script = "builtin:rewrite.chat-completions-to-responses.js"
timeout = "200ms"
reload = "watch"
```

| 字段 | 必填 | 默认行为 | 说明 |
|---|---:|---|---|
| `script` | 是 | 无 | 磁盘 `.js` 路径或 `builtin:<文件名>`。 |
| `timeout` | 否 | 继承 `--script-timeout`，默认 `200ms` | 每个 Hook 的执行超时，必须大于零。 |
| `reload` | 否 | 继承 `--script-reload`，默认 `watch` | 可选 `watch`、`poll`、`off`。 |

Profile 名称必须匹配：

```text
^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$
```

选择方式：

```bash
curl "http://127.0.0.1:7080/@openai/https://api.example.com/v1/responses"
curl "http://127.0.0.1:7080/team-a/@mock/https://example.com/healthz"
```

`openai-compat` 示例将 Chat Completions 请求转为上游 Responses；`stream: true` 时会实时转换 SSE，
并实时转回 Chat Completions chunks：

```bash
curl -N http://127.0.0.1:7080/@openai-compat/https://api.openai.com/v1/chat/completions \
  -H 'Authorization: Bearer $OPENAI_API_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

行为说明：

- 未指定 `@profile` 时使用 `--script` 配置的默认脚本。
- 指定 Profile 后只执行该 Profile，不与默认脚本组合。
- 未知 Profile 返回 `404`，不会回退默认脚本。
- Profile 只在 regular 模式参与路由；reverse 模式把 `@profile` 当作普通上游路径。
- `builtin:` 脚本已编入二进制，不依赖外部文件，也不支持热更新；即使配置
  `watch`，运行时也会按 `off` 处理。
- Profile 不是认证机制，不保护 Relay 写入端口。

## Web UI：`[web]`

```toml
[web]
max_transactions_per_namespace = 100
```

| 字段 | 默认值 | 说明 |
|---|---:|---|
| `max_transactions_per_namespace` | `100` | 每个 namespace 独立保留的最大交易记录数；默认视图也单独计数。必须大于零。 |

环境变量 `WEB_MAX_TRANSACTIONS_PER_NAMESPACE` 会覆盖 TOML 值：

```bash
WEB_MAX_TRANSACTIONS_PER_NAMESPACE=500 \
  http-relay --config ./http-relay.toml --web
```

## Web JWT 认证：`[web.auth]`

只有显式配置 `mode = "jwt"` 才启用 JWT。仅设置
`WEB_AUTH_JWT_SECRET` 不会自动启用 JWT 模式。

```toml
[web.auth]
mode = "jwt"
secret = "replace-with-unpadded-base64url-secret"
issuer = "http-relay"
audience = "http-relay-web"
token_ttl = "720h"
max_token_ttl = "2160h"
allow_permanent_tokens = false
admin_enabled = true
default_protected = true
fallback_protected = true
trust_forwarded_headers = false
```

| 字段 | 默认值 | 说明 |
|---|---:|---|
| `mode` | 空 | 只有 `jwt` 有效；空表示不启用 TOML JWT。显式配置空字符串会报错。 |
| `secret` | 空 | HS256 Secret。可由 `WEB_AUTH_JWT_SECRET` 覆盖。 |
| `issuer` | `http-relay` | JWT `iss`。不能为空。 |
| `audience` | `http-relay-web` | JWT `aud`。不能为空。 |
| `token_ttl` | `720h` | 签发 Token 的默认有效期。 |
| `max_token_ttl` | `2160h` | 允许签发的最大有效期，不能小于 `token_ttl`。 |
| `allow_permanent_tokens` | `false` | 是否允许签发和接受没有 `exp` 的永久 Token。 |
| `admin_enabled` | `false` | 是否启用 `/admin/` 和管理 API。 |
| `default_protected` | `false` | 默认视图 `/` 是否必须使用管理 Token。 |
| `fallback_protected` | `false` | 未在 namespaces 中声明的 namespace 是否受保护。 |
| `trust_forwarded_headers` | `false` | 是否信任 Web 反向代理传入的 `X-Forwarded-Proto` 和 `X-Forwarded-Host`。 |

Secret 要求：

- 必须是无 padding 的规范 Base64URL。
- 解码后至少 32 字节。
- 推荐使用 `http-relay-auth secret` 生成。
- 配置文件内包含 Secret 且权限宽于 `0600` 时，启动会输出安全警告。
- 生产环境优先通过 `WEB_AUTH_JWT_SECRET` 注入。

```bash
http-relay-auth secret
WEB_AUTH_JWT_SECRET='<生成值>' \
  http-relay --config ./http-relay.toml --web
```

`--web-trust-forwarded-headers` 与 TOML
`web.auth.trust_forwarded_headers` 是“任一开启即生效”的关系。只应在 Web 服务位于
可信反向代理之后时开启，否则客户端可以伪造转发 Header。

### namespace 保护策略

```toml
[web.auth.namespaces]
team-a = true
team-b = true
public-demo = false
```

- `true`：该 namespace 需要匹配 namespace 的 JWT，管理 Token 也可以访问。
- `false`：该 namespace 公开；合法管理 Token 仍然可以访问。
- 未声明的 namespace 使用 `fallback_protected`。
- 无 namespace 的默认视图使用 `default_protected`。
- 空 namespace 的 JWT 是管理 Token；不使用 `*` 表示管理权限。
- namespace 名称与 Profile 使用相同的单段命名规则。

管理 Token 和 namespace Token 示例：

```bash
http-relay-auth issue --config ./http-relay.toml --admin
http-relay-auth issue --config ./http-relay.toml --namespace team-a
http-relay-auth inspect --config ./http-relay.toml '<token>'
```

### 与旧版 `WEB_AUTH_KEY` 的关系

未启用 TOML JWT 时，设置 `WEB_AUTH_KEY` 会启用旧版全局 Web 密码认证，会话默认
有效 24 小时。JWT 模式与 `WEB_AUTH_KEY` 不能同时使用，同时存在时程序启动失败。

JWT 和旧版密码都只保护 Web 页面、SSE 和 Web API，不保护 Relay 端口的流量写入。
Relay 端口应通过监听地址、防火墙或可信反向代理限制访问。

## 环境变量覆盖关系

| 环境变量 | 作用 | 优先级/注意事项 |
|---|---|---|
| `HTTP_RELAY_CONFIG` | TOML 配置路径 | 低于 `--config`。 |
| `WEB_AUTH_JWT_SECRET` | JWT HMAC Secret | 高于 `web.auth.secret`，但不会单独启用 JWT。 |
| `WEB_MAX_TRANSACTIONS_PER_NAMESPACE` | 每 namespace 记录上限 | 高于 `[web]` 中的值，必须是正整数。 |
| `WEB_AUTH_KEY` | 旧版全局 Web 密码 | 仅在未启用 JWT 时使用；与 JWT 同时存在会失败。 |
| `HOST` / `PORT` | Relay 监听地址回退值 | 会被 `--listen` 或对应命令参数覆盖。 |
| `WIRE_SCOPE` | dump scope 回退值 | 会被 `--dump-scope` 覆盖。 |
| `ALL_PROXY` / `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | Relay 上游代理 | 不影响 `relay.http.request()` 的独立 Client。 |

regular 模式默认会拒绝路径指定的本地、私网和解析到这些地址的目标，并直接连接到
已校验的 IP；因此这类受保护请求会绕过上游代理。仅在受信任环境确实需要访问内网上游
时，使用 CLI 参数 `--allow-private-targets` 恢复原有的内网目标和代理行为。

## 完整示例

```toml
[rewrite.http]
enabled = true
allowed_origins = ["https://config.example.com"]
timeout = "800ms"
max_timeout = "1s"
max_request_body_bytes = 1048576
max_response_body_bytes = 1048576
max_calls_per_hook = 3
follow_redirects = false
allow_private_networks = false

[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
timeout = "1500ms"
reload = "off"

[rewrite.profiles.mock]
script = "./plugins/examples/rewrite.mock.js"
timeout = "300ms"
reload = "watch"

[web]
max_transactions_per_namespace = 200

[web.auth]
mode = "jwt"
# 更推荐通过 WEB_AUTH_JWT_SECRET 注入
# secret = "replace-with-http-relay-auth-secret-output"
issuer = "http-relay"
audience = "http-relay-web"
token_ttl = "720h"
max_token_ttl = "2160h"
allow_permanent_tokens = false
admin_enabled = true
default_protected = true
fallback_protected = true
trust_forwarded_headers = false

[web.auth.namespaces]
team-a = true
team-b = true
public-demo = false
```

启动：

```bash
WEB_AUTH_JWT_SECRET='<http-relay-auth secret 输出>' \
  http-relay --config ./http-relay.toml --web
```

## 常见配置错误

- `config.toml` 放在当前目录但未传 `--config`：程序不会自动发现它。
- `enabled = true` 但 `allowed_origins = []`：脚本 HTTP 配置校验失败。
- `allowed_origins` 写成完整 API URL：只能写 origin，不能包含 `/v1/path`。
- Profile 相对脚本路径按进程目录编写：实际基准是配置文件所在目录。
- 内置脚本配置 `reload = "watch"`：内置资源不可热更新，运行时会强制关闭。
- 外部 HTTP timeout 大于 Hook timeout：请求会先被 Hook deadline 取消。
- JWT Secret 使用普通密码、带 `=` padding 或不足 32 字节：启动会失败。
- 同时设置 `WEB_AUTH_KEY` 和 `web.auth.mode = "jwt"`：两种认证模式冲突。
- 在不可信网络环境开启 `trust_forwarded_headers`：可能信任伪造的转发 Header。
