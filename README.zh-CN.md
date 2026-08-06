<div align="center">

# http-relay

轻量 HTTP 转发工具。

[![CI](https://github.com/onewesong/http-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/onewesong/http-relay/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/onewesong/http-relay)](https://github.com/onewesong/http-relay/releases)
[![Docker Image](https://img.shields.io/badge/ghcr.io-onewesong%2Fhttp--relay-blue)](https://github.com/onewesong/http-relay/pkgs/container/http-relay)
[![License](https://img.shields.io/github/license/onewesong/http-relay)](https://github.com/onewesong/http-relay/blob/main/LICENSE)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/onewesong/http-relay)

[English](./README.md) | 简体中文

</div>

`http-relay` 监听本地 HTTP，请求格式如下：

`http://localhost:{port}/https://example.com/path?...`

它会将路径中的绝对 URL 作为上游目标进行转发，并原样返回上游响应（状态码、响应头、响应体）。

## 安装

安装最新版 Release 二进制：

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | sh
```

安装指定版本，或安装到当前用户可写目录：

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | VERSION=v1.2.3 sh
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | BINDIR="$HOME/.local/bin" sh
```

从源码安装：

```bash
go install github.com/onewesong/http-relay/cmd/http-relay@latest
go install github.com/onewesong/http-relay/cmd/http-relay-auth@latest
```

Docker：

```bash
docker run --rm -p 7080:7080 ghcr.io/onewesong/http-relay:latest
```

GitHub Actions 镜像发布规则：

- 推送到 `main`：发布 `ghcr.io/onewesong/http-relay:edge` 和 `sha-*`
- 推送 `v1.2.3` 这类标签：发布 `v1.2.3`、`1.2`、`1`、`latest`

## 快速开始

1. 启动服务（默认 `127.0.0.1:7080`）：

```bash
http-relay
```

2. 发起请求：

```bash
curl -i "http://127.0.0.1:7080/https://example.com"
```

查看版本：

```bash
http-relay version
```

反向代理到固定上游：

```bash
http-relay --mode reverse:https://api.example.com
curl -i "http://127.0.0.1:7080/v1/users"
```

上面的请求会转发到 `https://api.example.com/v1/users`。

## 命令参数

- `--mode`：转发模式，支持 `regular`（默认）和 `reverse:<url>`
- `--config`：TOML 配置文件；未指定时读取 `HTTP_RELAY_CONFIG`
- `--listen`：监听地址，优先级高于 `--host` / `--port`
- `--host`：监听主机（默认读取 `HOST`，否则 `127.0.0.1`）
- `--port`：监听端口（默认读取 `PORT`，否则 `7080`）
- `--timeout`：上游请求超时（默认 `120s`）
- `-w` / `--dump`：输出请求/响应转储
- `--dump-scope`：转储范围，支持 `req`、`resp`、`req,resp`
- `--mask-auth`：请求转储时脱敏认证相关请求头
- `--tui`：交互式可折叠界面；逐条列出请求，方向键 / `j`、`k` 选择，`enter` 展开该请求的头部与正文，`q` 退出（隐式开启 req+resp 转储，需要在终端中运行）
- `--web`：启动实时 Web 界面，通过 SSE 把流量推送到浏览器；响应正文可在 Preview/Raw 间切换，Preview 支持可折叠 JSON、沙箱 HTML 和 SSE/OpenAI 消息合并；Conversations 视图可按显式会话 ID、`previous_response_id` 或完整消息历史关联 OpenAI 连续对话，并可跳回原始请求（隐式开启 req+resp 转储，监听在独立端口）
- `--web-listen`：Web 界面监听地址（默认 `127.0.0.1:7090`）
- `--web-trust-forwarded-headers`：信任反向代理传入的 `X-Forwarded-Proto` / `X-Forwarded-Host`
- `--add-header`：给上游请求追加请求头，可重复
- `--modify-header`：给上游请求设置/覆盖请求头，可重复

示例：

```bash
http-relay --listen 0.0.0.0:9000
http-relay --mode reverse:https://api.example.com --timeout 30s
```

## 配置（环境变量）

- `HOST`：监听地址（默认 `127.0.0.1`）
- `PORT`：监听端口（默认 `7080`）
- `WIRE_SCOPE`：`--dump-scope` 的兼容环境变量
- `HTTP_RELAY_CONFIG`：TOML 配置文件路径，优先级低于 `--config`
- `WEB_AUTH_KEY`：Web 界面登录密钥；仅在使用 `--web` 时生效。未设置或为空时不启用认证；设置后页面、SSE 和交易 API 需要登录，会话有效期为 24 小时。
- `WEB_AUTH_JWT_SECRET`：覆盖 TOML 中的 JWT Secret；它不会单独启用 JWT 模式。

Web 认证的 Docker Compose 示例：

```yaml
services:
  http-relay:
    image: ghcr.io/onewesong/http-relay:latest
    command: ["--listen", "0.0.0.0:7080", "--web", "--web-listen", "0.0.0.0:7090"]
    environment:
      WEB_AUTH_KEY: "replace-with-a-long-random-secret"
    ports:
      - "127.0.0.1:7080:7080"
      - "127.0.0.1:7090:7090"
```

### Namespace JWT 认证

JWT 模式可分别保护默认视图和每个 namespace。复制 [config.example.toml](config.example.toml)，然后运行 `http-relay-auth secret` 生成 Secret。完整配置如下：

```toml
[web.auth]
mode = "jwt"
secret = "replace-with-http-relay-auth-secret-output"
issuer = "http-relay"
audience = "http-relay-web"
token_ttl = "720h"
max_token_ttl = "2160h"
allow_permanent_tokens = true
admin_enabled = true
default_protected = true
fallback_protected = false
trust_forwarded_headers = false

[web.auth.namespaces]
team-a = true
team-b = true
public-demo = false
```

Secret 必须是至少 32 字节随机数据的无 padding Base64URL 编码。配置内嵌 Secret 时应执行 `chmod 600 http-relay.toml`；生产环境更推荐通过 `WEB_AUTH_JWT_SECRET` 覆盖。JWT 模式不能与 `WEB_AUTH_KEY` 同时使用。

启动 Web，并离线创建管理 Token：

```bash
http-relay --config ./http-relay.toml --web
http-relay-auth issue --config ./http-relay.toml --admin
```

在 `/login` 粘贴管理 Token 后会进入 `/admin/`。管理页可以查看所有 namespace 的记录数、最后记录时间和 SSE 订阅数，并签发仅限单一非空 namespace 的 Token。管理页不能签发新的管理 Token；管理 Token 始终通过 `http-relay-auth issue --admin` 离线创建。

命令行也可以签发和检查受限 Token：

```bash
http-relay-auth issue --config ./http-relay.toml --namespace team-a --ttl 24h
http-relay-auth issue --config ./http-relay.toml --namespace team-a --permanent
printf '%s' "$TOKEN" | http-relay-auth inspect --config ./http-relay.toml -
```

`--permanent` 只有在 `allow_permanent_tokens = true` 时可用，生成的 JWT 不含 `exp`。永久 Token 仍可能因浏览器清理 Cookie 而需要重新登录。第一版不支持单 Token 吊销；轮换 Secret 并重启服务会使全部旧 JWT 失效，之后应离线创建新的管理 Token 并重新签发受限 Token。请像密码一样保管管理 Token，不要放入 URL、日志或 shell history。

JWT 只保护 Web 端口上的页面、SSE、查询和 Clear 操作，不保护 Relay 端口的请求写入。任何能访问 Relay 端口的客户端仍可写入记录，因此公网部署时还应在反向代理或网络层限制 Relay 端口。

Docker Compose 可以把含 Secret 的完整 TOML 作为 Docker Secret 挂载：

```yaml
services:
  http-relay:
    image: ghcr.io/onewesong/http-relay:latest
    command: ["--config", "/run/secrets/http_relay_config", "--listen", "0.0.0.0:7080", "--web", "--web-listen", "0.0.0.0:7090"]
    secrets: [http_relay_config]
    ports:
      - "127.0.0.1:7080:7080"
      - "127.0.0.1:7090:7090"
secrets:
  http_relay_config:
    file: ./http-relay.toml
```

也可以挂载不含 Secret 的示例配置，并通过环境变量注入：

```yaml
environment:
  HTTP_RELAY_CONFIG: /etc/http-relay/http-relay.toml
  WEB_AUTH_JWT_SECRET: "${WEB_AUTH_JWT_SECRET}"
volumes:
  - ./config.example.toml:/etc/http-relay/http-relay.toml:ro
```

### 响应预览实验台

开发预览插件时，可以启动不连接代理流量的本地实验台：

```bash
go run ./cmd/preview-lab
```

默认访问地址为 `http://127.0.0.1:8091`。页面内置 JSON、HTML、SSE、OpenAI 流式响应、文本和二进制用例，可以编辑响应头与正文并即时切换 Preview/Raw。使用 `-listen` 修改监听地址；实验台只用于本地开发，不包含在正式 Web UI 中。

通过 Nginx 使用 HTTPS 反向代理时，请传递以下请求头，并仅在该代理可信时启用 `trust_forwarded_headers` 或 `--web-trust-forwarded-headers`：

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-Host $host;
```

## 抓包输出

开启请求/响应转储：

```bash
http-relay -w
```

对请求头认证信息脱敏：

```bash
http-relay -w -mask-auth
```

会脱敏的请求头：
`Authorization`、`Proxy-Authorization`、`Cookie`、`X-Api-Key`、`X-Auth-Token`。

使用 `WIRE_SCOPE` 控制输出范围（仅 `-w` 开启时生效）：

- `req`：只输出请求
- `resp`：只输出响应
- `req,resp`：请求和响应都输出（默认）

示例：

```bash
WIRE_SCOPE=req http-relay -w
WIRE_SCOPE=resp http-relay -w
WIRE_SCOPE=req,resp http-relay -w
http-relay --dump --dump-scope req,resp
```

## 请求头改写

追加请求头：

```bash
http-relay --add-header "X-Debug: 1"
```

设置或覆盖请求头：

```bash
http-relay --modify-header "User-Agent: http-relay"
```

组合反向代理使用：

```bash
http-relay \
  --mode reverse:https://api.example.com \
  --add-header "X-Trace-Source: local" \
  --modify-header "User-Agent: http-relay"
```

## 上游代理

支持标准代理环境变量：

- `ALL_PROXY`（优先级最高）
- `HTTP_PROXY` / `HTTPS_PROXY`
- `NO_PROXY`（命中后直连）

示例：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 http-relay
ALL_PROXY=socks5://127.0.0.1:1080 http-relay
HTTPS_PROXY=http://127.0.0.1:7890 NO_PROXY=example.com http-relay
```

## 路由规则

默认 `regular` 模式支持 `/{absolute-url}`，例如：

- `http://127.0.0.1:7080/https://example.com`
- `http://127.0.0.1:7080/http://httpbin.org/post`

也可以在目标 URL 前增加单段 namespace，用于在 Web 界面中隔离查看不同来源的请求：

```bash
curl -i "http://127.0.0.1:7080/team-a/https://example.com"
curl -i "http://127.0.0.1:7080/team-b/https://example.com"
```

namespace 不会发送给上游，因此第一个请求的上游目标仍是 `https://example.com`。使用 `--web` 时，分别访问：

- `http://127.0.0.1:7090/`：查看不带 namespace 的请求
- `http://127.0.0.1:7090/namespace/team-a/`：仅查看 `team-a` 请求
- `http://127.0.0.1:7090/namespace/team-b/`：仅查看 `team-b` 请求

namespace 只支持字母、数字、点、下划线和连字符，长度最多 64 个字符且必须以字母或数字开头。Web 旧路径（如 `/team-a/`）不再兼容，也不会自动重定向。未启用 JWT 时 namespace 只是流量分组；启用 JWT 后 Web 读取和清理具有权限隔离。反向代理模式不会解析 namespace，路径会完整转发给固定上游。

目标 URL 必须包含 `http://` 或 `https://`。

`reverse:<url>` 模式会将原始路径和查询参数拼接到固定上游，例如：

```bash
http-relay --mode reverse:https://api.example.com/base
curl "http://127.0.0.1:7080/v1/users?q=go"
```

转发目标为 `https://api.example.com/base/v1/users?q=go`。

## 错误码

- `400`：目标 URL 缺失或格式错误
- `502`：上游连接失败或超时
- `500`：服务内部错误
