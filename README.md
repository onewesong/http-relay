# http-relay

一个用 Go 编写的轻量 HTTP 转发工具。

当你请求本服务：

`http://localhost:{port}/https://example.com/path?...`

它会把请求转发到路径中携带的上游绝对 URL，并将上游响应原样回传（状态码、响应头、响应体）。

## 特性

- 监听地址使用环境变量 `HOST` + `PORT` 控制（无配置文件、无命令行参数）。
- 路由格式固定为 `/{absolute-url}`。
- 透明透传所有 HTTP 方法（GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS 等）和请求体。
- 支持通过环境变量使用 HTTP 代理或 SOCKS5 代理访问上游。
- 支持 `NO_PROXY` 直连排除。
- 过滤 Hop-by-Hop 头并补充 `X-Forwarded-*` 头。

## 环境要求

- Go 1.22+

## 快速开始

1. 启动服务（默认监听 `127.0.0.1:8080`）：

```bash
go run ./cmd/http-relay
```

2. 发起转发请求：

```bash
curl -i "http://127.0.0.1:8080/https://example.com"
```

开启请求抓包输出（打印收到的请求头和 body）：

```bash
go run ./cmd/http-relay -w
```

## 监听配置

使用以下环境变量：

- `HOST`：默认 `127.0.0.1`
- `PORT`：默认 `8080`

示例：

```bash
HOST=0.0.0.0 PORT=9000 go run ./cmd/http-relay
```

## 启动参数

- `-w`：开启流量转储输出（配合 `WIRE_SCOPE` 控制范围）。

### `WIRE_SCOPE`（仅在 `-w` 开启时生效）

- `req`：只打印请求
- `resp`：只打印响应
- `req,resp`：同时打印请求和响应（默认）

示例：

```bash
WIRE_SCOPE=req go run ./cmd/http-relay -w
WIRE_SCOPE=resp go run ./cmd/http-relay -w
WIRE_SCOPE=req,resp go run ./cmd/http-relay -w
```

## 路由规则

仅支持 `/{absolute-url}`，例如：

- `http://127.0.0.1:8080/https://example.com`
- `http://127.0.0.1:8080/http://httpbin.org/post`

要求：

- 目标 URL 必须包含完整 scheme（`http://` 或 `https://`）。
- 其他 scheme（如 `ftp://`）会返回 `400 Bad Request`。

## 上游代理（环境变量）

支持标准代理变量，优先级如下：

1. `ALL_PROXY`（最高优先级）
2. `HTTP_PROXY` / `HTTPS_PROXY`（按目标 URL 协议选择）
3. `NO_PROXY`（命中则直连）

同时兼容小写变量（如 `all_proxy`）。

### HTTP 代理示例

```bash
HTTPS_PROXY=http://127.0.0.1:7890 go run ./cmd/http-relay
```

### SOCKS5 代理示例

```bash
ALL_PROXY=socks5://127.0.0.1:1080 go run ./cmd/http-relay
```

### NO_PROXY 示例

```bash
HTTPS_PROXY=http://127.0.0.1:7890 NO_PROXY=example.com go run ./cmd/http-relay
```

## 请求与响应行为

- 请求方法：全部透传。
- 请求头：透传常规头，移除 Hop-by-Hop 头：
  - `Connection`
  - `Keep-Alive`
  - `Proxy-Authenticate`
  - `Proxy-Authorization`
  - `TE`
  - `Trailer`
  - `Transfer-Encoding`
  - `Upgrade`
- 附加/更新头：
  - `X-Forwarded-For`
  - `X-Forwarded-Proto`（固定为 `http`）
  - `X-Forwarded-Host`
- 响应：上游状态码、响应头、响应体回传（响应头同样移除 Hop-by-Hop 头）。

## 错误码约定

- `400 Bad Request`：目标 URL 缺失或非法（如未带 scheme）。
- `502 Bad Gateway`：上游连接失败、网络错误或超时。
- `500 Internal Server Error`：构建上游请求等内部错误。

## 日志

- 启动时打印监听地址与代理模式摘要（会脱敏，不打印代理凭证）。
- 每个请求打印：方法、目标 URL、状态码、耗时、错误信息。

## 测试

运行全部测试：

```bash
go test ./...
```

## 限制

- 不提供鉴权与访问控制（定位为本地/内网工具）。
- 不支持 CONNECT 隧道，仅做应用层 HTTP 请求转发。
- 不支持配置文件或命令行参数，统一环境变量控制。
