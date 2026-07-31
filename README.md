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
support web mode
<img width="1896" height="943" alt="image" src="https://github.com/user-attachments/assets/19acbf84-23f5-4199-ae64-4f70faae6e6f" />
can auto merge conversation
<img width="1879" height="931" alt="image" src="https://github.com/user-attachments/assets/b8822a9d-a8ca-48bc-96d9-e055855ed558" />


`http-relay` listens on local HTTP and relays requests in this format:

`http://localhost:{port}/https://example.com/path?...`

It forwards the request to the target absolute URL in the path and returns the upstream response as-is (status code, headers, body).

## Installation

Install the latest release binary:

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | sh
```

Install a specific version or install into a user-writable directory:

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | VERSION=v1.2.3 sh
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | BINDIR="$HOME/.local/bin" sh
```

Build from source:

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
- `--tui`: interactive collapsible TUI; lists each request, arrow keys / `j`,`k` to select, `enter` to expand its headers and body, `q` to quit (implies dumping req+resp, requires a terminal)
- `--web`: serve a live web UI that streams traffic to the browser over SSE; response bodies switch between Preview and Raw, with collapsible JSON, sandboxed HTML, and merged SSE/OpenAI messages; the Conversations view links OpenAI turns by explicit conversation IDs, `previous_response_id`, or complete message history and links back to source requests (implies dumping req+resp, served on a separate port)
- `--web-listen`: listen address for the web UI (default: `127.0.0.1:8090`)
- `--add-header`: add an upstream request header, repeatable
- `--modify-header`: set/overwrite an upstream request header, repeatable
- `--script`: path to a JavaScript file with `onRequest` / `onResponse` hooks that rewrite traffic
- `--script-timeout`: per-hook execution timeout (default: `200ms`)
- `--script-reload`: hot-reload mode, supports `watch` (default), `poll`, `off`

Example:

```bash
http-relay --listen 0.0.0.0:9000
http-relay --mode reverse:https://api.example.com --timeout 30s
```

## Configuration (Environment Variables)

- `HOST`: listen host (default: `127.0.0.1`)
- `PORT`: listen port (default: `8080`)
- `WIRE_SCOPE`: compatibility fallback for `--dump-scope`
- `WEB_AUTH_KEY`: login key for the Web UI, effective only with `--web`. Empty or unset keeps the UI public; when set, the page, SSE, and transaction API require login and sessions last 24 hours.

Docker Compose example with Web authentication:

```yaml
services:
  http-relay:
    image: ghcr.io/onewesong/http-relay:latest
    command: ["--listen", "0.0.0.0:8080", "--web", "--web-listen", "0.0.0.0:8090"]
    environment:
      WEB_AUTH_KEY: "replace-with-a-long-random-secret"
    ports:
      - "127.0.0.1:8080:8080"
      - "127.0.0.1:8090:8090"
```

### Response preview lab

When developing preview plugins, start the local workbench without connecting it to proxy traffic:

```bash
go run ./cmd/preview-lab
```

It listens at `http://127.0.0.1:8091` by default. The page includes editable JSON, HTML, SSE, OpenAI streaming, text, and binary fixtures with instant Preview/Raw switching. Use `-listen` to change the address. The lab is development-only and is not included in the production Web UI assets.

When HTTPS is terminated by Nginx, forward `X-Forwarded-Proto` so the session cookie gets the `Secure` attribute:

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
```

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

## Script Rewrite (JavaScript)

For logic beyond static header rules, point `--script` at a JavaScript file that
exports one or both hook functions. They run inside an embedded ECMAScript
engine ([goja](https://github.com/dop251/goja)) — no external runtime needed.

```bash
http-relay --script ./examples/relay.example.js
```

```js
// Rewrite the request sent upstream; return an object to short-circuit
// (skip upstream and reply directly).
function onRequest(req) {
  req.headers["X-Trace-Id"] = "trace-" + Date.now();
  delete req.headers["Cookie"];

  // Reroute to a new API version.
  if (req.url.indexOf("/api/v1/") >= 0) {
    req.url = req.url.replace("/api/v1/", "/api/v2/");
  }

  // Local mock — never hits upstream.
  if (req.url.indexOf("/healthz") >= 0) {
    return { status: 200, headers: { "Content-Type": "application/json" }, body: '{"ok":true}' };
  }
}

// Rewrite the response returned to the client.
function onResponse(resp, req) {
  resp.headers["X-Proxied-By"] = "http-relay";
  if (resp.status === 500) {
    resp.status = 503;
    resp.body = "service temporarily unavailable\n";
  }
}
```

Hook object model (mutate in place to take effect):

- `req.method` / `req.url` / `req.host` — strings. Rewriting `req.url` reroutes the
  request; the script has the final say on the target (it overrides `--mode`).
- `req.headers` / `resp.headers` — plain objects keyed by canonical header name:
  - `h["X-Foo"] = "v"` — add or overwrite
  - `delete h["X-Foo"]` — remove the header
  - `h["X-Foo"] = ""` — keep the header with an empty value
- `req.body` / `resp.body` — strings. `Content-Length` is recomputed automatically.
- `resp.status` — number.
- `onRequest` may `return { status, headers, body }` to short-circuit. `onResponse`
  still runs on the synthesized response, so it can post-process mocks too.
- `console.log` / `info` / `warn` / `error` / `debug` write to stderr (silenced under `--tui`).

Behavior notes:

- Both hooks are optional; an absent hook is skipped.
- A hook that throws or exceeds `--script-timeout` returns `500` and does **not**
  reach upstream.
- A script that fails to compile at startup is fatal (the process exits).
- `--script-reload` controls hot-reload: `watch` reloads on file changes (including
  editor atomic saves), `poll` checks the modification time periodically, `off`
  loads once at startup. A reload that fails to compile keeps the previous version
  serving traffic.
- Scripting works in all modes, including `--tui` and `--web`.

See [examples/relay.example.js](examples/relay.example.js) for a fuller example.

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


