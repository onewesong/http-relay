<div align="center">

# http-relay

A lightweight HTTP relay tool.

[![CI](https://github.com/onewesong/http-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/onewesong/http-relay/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/onewesong/http-relay)](https://github.com/onewesong/http-relay/releases)
[![Docker Image](https://img.shields.io/badge/ghcr.io-onewesong%2Fhttp--relay-blue)](https://github.com/onewesong/http-relay/pkgs/container/http-relay)
[![License](https://img.shields.io/github/license/onewesong/http-relay)](https://github.com/onewesong/http-relay/blob/main/LICENSE)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/onewesong/http-relay)
<a href="https://llmapis.com?source=https%3A%2F%2Fgithub.com%2Fonewesong%2Fhttp-relay" target="_blank"><img src="https://llmapis.com/api/badge/onewesong/http-relay" alt="LLMAPIS" width="20" /></a>

English | [简体中文](./README.zh-CN.md)

</div>
<img width="1470" height="887" alt="image" src="https://github.com/user-attachments/assets/93c52569-12d5-44cc-9bcf-81224a101a90" />

`http-relay` listens on local HTTP and relays requests in this format:

`http://localhost:{port}/https://example.com/path?...`

It forwards the request to the target absolute URL in the path and returns the upstream response as-is (status code, headers, body).

## Installation

```bash
go install github.com/onewesong/http-relay/cmd/http-relay@latest
```

Docker:

```bash
docker run --rm -p 8080:8080 ghcr.io/onewesong/http-relay:latest
```

GitHub Actions image publishing:

- push to `main`: publish `ghcr.io/onewesong/http-relay:edge` and `sha-*`
- push tag like `v1.2.3`: publish `v1.2.3`, `1.2`, `1`, `latest`

## Quick Start

1. Start service (default `127.0.0.1:8080`):

```bash
http-relay
```

2. Send a request:

```bash
curl -i "http://127.0.0.1:8080/https://example.com"
```

Check version:

```bash
http-relay version
```

Reverse proxy to a fixed upstream:

```bash
http-relay --mode reverse:https://api.example.com
curl -i "http://127.0.0.1:8080/v1/users"
```

The request above is forwarded to `https://api.example.com/v1/users`.

## Command Options

- `--mode`: target mode, supports `regular` (default) and `reverse:<url>`
- `--listen`: listen address, overrides `--host` / `--port`
- `--host`: listen host (defaults to `HOST`, then `127.0.0.1`)
- `--port`: listen port (defaults to `PORT`, then `8080`)
- `--timeout`: upstream request timeout (default: `120s`)
- `-w` / `--dump`: dump request/response traffic
- `--dump-scope`: dump scope, supports `req`, `resp`, `req,resp`
- `--mask-auth`: mask auth-related request headers in request dump
- `--add-header`: add an upstream request header, repeatable
- `--modify-header`: set/overwrite an upstream request header, repeatable

Example:

```bash
http-relay --listen 0.0.0.0:9000
http-relay --mode reverse:https://api.example.com --timeout 30s
```

## Configuration (Environment Variables)

- `HOST`: listen host (default: `127.0.0.1`)
- `PORT`: listen port (default: `8080`)
- `WIRE_SCOPE`: compatibility fallback for `--dump-scope`

## Traffic Dump

Enable request/response dump:

```bash
http-relay -w
```

Mask auth-related headers in request dump:

```bash
http-relay -w -mask-auth
```

Masked request headers:
`Authorization`, `Proxy-Authorization`, `Cookie`, `X-Api-Key`, `X-Auth-Token`.

Use `WIRE_SCOPE` (effective only when `-w` is enabled):

- `req`: dump request only
- `resp`: dump response only
- `req,resp`: dump both (default)

Examples:

```bash
WIRE_SCOPE=req http-relay -w
WIRE_SCOPE=resp http-relay -w
WIRE_SCOPE=req,resp http-relay -w
http-relay --dump --dump-scope req,resp
```

## Header Rewrite

Add a request header:

```bash
http-relay --add-header "X-Debug: 1"
```

Set or overwrite a request header:

```bash
http-relay --modify-header "User-Agent: http-relay"
```

Use with reverse proxy mode:

```bash
http-relay \
  --mode reverse:https://api.example.com \
  --add-header "X-Trace-Source: local" \
  --modify-header "User-Agent: http-relay"
```

## Upstream Proxy

Supported proxy env vars:

- `ALL_PROXY` (highest priority)
- `HTTP_PROXY` / `HTTPS_PROXY`
- `NO_PROXY` (bypass proxy when matched)

Examples:

```bash
HTTPS_PROXY=http://127.0.0.1:7890 http-relay
ALL_PROXY=socks5://127.0.0.1:1080 http-relay
HTTPS_PROXY=http://127.0.0.1:7890 NO_PROXY=example.com http-relay
```

## Route Rule

Default `regular` mode supports `/{absolute-url}`, for example:

- `http://127.0.0.1:8080/https://example.com`
- `http://127.0.0.1:8080/http://httpbin.org/post`

Target URL must include `http://` or `https://`.

`reverse:<url>` mode joins the incoming path and query onto a fixed upstream:

```bash
http-relay --mode reverse:https://api.example.com/base
curl "http://127.0.0.1:8080/v1/users?q=go"
```

The target is `https://api.example.com/base/v1/users?q=go`.

## Error Codes

- `400`: missing or invalid target URL
- `502`: upstream connection failure or timeout
- `500`: internal server error


## Star History

<a href="https://www.star-history.com/">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/image?repos=onewesong/http-relay&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/image?repos=onewesong/http-relay&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/image?repos=onewesong/http-relay&type=date&legend=top-left" />
 </picture>
</a>
