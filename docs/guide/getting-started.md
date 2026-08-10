# 安装与快速开始

## 安装 Release 二进制

安装最新版本：

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | sh
```

安装指定版本或指定目录：

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | VERSION=v0.15.0 sh
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | BINDIR="$HOME/.local/bin" sh
```

## 使用 Go 安装

```bash
go install github.com/onewesong/http-relay/cmd/http-relay@latest
go install github.com/onewesong/http-relay/cmd/http-relay-auth@latest
```

## 使用 Docker

```bash
docker run --rm -p 7080:7080 ghcr.io/onewesong/http-relay:latest \
  --listen 0.0.0.0:7080
```

## 第一个转发请求

服务默认监听 `127.0.0.1:7080`：

```bash
http-relay
```

将完整上游 URL 放在 Relay 地址之后：

```bash
curl -i "http://127.0.0.1:7080/https://example.com/path?q=relay"
```

请求会被发送到 `https://example.com/path?q=relay`，上游状态码、响应头和响应体会返回给客户端。

## 启用 Web UI

```bash
http-relay --web
```

- Relay：`http://127.0.0.1:7080`
- Web UI：`http://127.0.0.1:7090`

`--web` 会自动采集请求和响应。Web 页面支持 JSON、HTML、SSE/OpenAI 流式响应预览，以及连续对话聚合。

## 查看版本与帮助

```bash
http-relay version
http-relay --help
```

下一步可以了解[转发模式与路由](/features/proxy-modes)，或者为流量编写[改写脚本](/scripting/getting-started)。
