# http-relay

一个轻量的 HTTP 转发工具。

请求本服务：

`http://localhost:{port}/https://example.com/path?...`

会转发到路径中的目标 URL，并把上游响应（状态码、响应头、响应体）返回给你。

## 安装

```bash
go install github.com/onewesong/http-relay/cmd/http-relay@latest
```

## 快速开始

1. 启动服务（默认监听 `127.0.0.1:8080`）：

```bash
http-relay
```

2. 发起转发请求：

```bash
curl -i "http://127.0.0.1:8080/https://example.com"
```

查看版本：

```bash
http-relay version
```

## 基本配置（环境变量）

- `HOST`：监听地址，默认 `127.0.0.1`
- `PORT`：监听端口，默认 `8080`

示例：

```bash
HOST=0.0.0.0 PORT=9000 http-relay
```

## 调试输出

使用 `-w` 开启请求/响应转储输出：

```bash
http-relay -w
```

可用 `WIRE_SCOPE` 控制输出范围（仅在 `-w` 开启时生效）：

- `req`：只输出请求
- `resp`：只输出响应
- `req,resp`：请求和响应都输出（默认）

示例：

```bash
WIRE_SCOPE=req http-relay -w
WIRE_SCOPE=resp http-relay -w
WIRE_SCOPE=req,resp http-relay -w
```

## 上游代理

支持标准代理环境变量：

- `ALL_PROXY`（优先级最高）
- `HTTP_PROXY` / `HTTPS_PROXY`
- `NO_PROXY`（命中则直连）

示例：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 http-relay
ALL_PROXY=socks5://127.0.0.1:1080 http-relay
HTTPS_PROXY=http://127.0.0.1:7890 NO_PROXY=example.com http-relay
```

## 路由规则

仅支持 `/{absolute-url}`，例如：

- `http://127.0.0.1:8080/https://example.com`
- `http://127.0.0.1:8080/http://httpbin.org/post`

要求目标 URL 必须带 `http://` 或 `https://`。

## 错误码

- `400`：目标 URL 缺失或格式错误
- `502`：上游连接失败或超时
- `500`：服务内部错误
