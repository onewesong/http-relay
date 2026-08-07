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
go install github.com/onewesong/http-relay/cmd/http-relay-auth@latest
```

Docker:

```bash
docker run --rm -p 7080:7080 ghcr.io/onewesong/http-relay:latest
```

GitHub Actions image publishing:

- push to `main`: publish `ghcr.io/onewesong/http-relay:edge` and `sha-*`
- push tag like `v1.2.3`: publish `v1.2.3`, `1.2`, `1`, `latest`

## Quick Start

1. Start service (default `127.0.0.1:7080`):

```bash
http-relay
```

2. Send a request:

```bash
curl -i "http://127.0.0.1:7080/https://example.com"
```

Check version:

```bash
http-relay version
```

Reverse proxy to a fixed upstream:

```bash
http-relay --mode reverse:https://api.example.com
curl -i "http://127.0.0.1:7080/v1/users"
```

The request above is forwarded to `https://api.example.com/v1/users`.

## Command Options

- `--mode`: target mode, supports `regular` (default) and `reverse:<url>`
- `--config`: TOML configuration path; falls back to `HTTP_RELAY_CONFIG`
- `--listen`: listen address, overrides `--host` / `--port`
- `--host`: listen host (defaults to `HOST`, then `127.0.0.1`)
- `--port`: listen port (defaults to `PORT`, then `7080`)
- `--timeout`: upstream request timeout (default: `120s`)
- `-w` / `--dump`: dump request/response traffic
- `--dump-scope`: dump scope, supports `req`, `resp`, `req,resp`
- `--mask-auth`: mask auth-related request headers in request dump
- `--tui`: interactive collapsible TUI; lists each request, arrow keys / `j`,`k` to select, `enter` to expand its headers and body, `q` to quit (implies dumping req+resp, requires a terminal)
- `--web`: serve a live web UI that streams traffic to the browser over SSE; response bodies switch between Preview and Raw, with collapsible JSON, sandboxed HTML, and merged SSE/OpenAI messages; the Conversations view links OpenAI turns by explicit conversation IDs, `previous_response_id`, or complete message history and links back to source requests (implies dumping req+resp, served on a separate port)
- `--web-listen`: listen address for the web UI (default: `127.0.0.1:7090`)
- `--web-trust-forwarded-headers`: trust reverse-proxy `X-Forwarded-Proto` / `X-Forwarded-Host`
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
- `PORT`: listen port (default: `7080`)
- `WIRE_SCOPE`: compatibility fallback for `--dump-scope`
- `HTTP_RELAY_CONFIG`: TOML configuration path, overridden by `--config`
- `WEB_AUTH_KEY`: login key for the Web UI, effective only with `--web`. Empty or unset keeps the UI public; when set, the page, SSE, and transaction API require login and sessions last 24 hours.
- `WEB_AUTH_JWT_SECRET`: overrides the JWT secret in TOML; it does not enable JWT mode by itself.
- `WEB_MAX_TRANSACTIONS_PER_NAMESPACE`: maximum retained transactions per namespace, defaults to `100` and overrides TOML.

Docker Compose example with Web authentication:

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

### Namespace JWT authentication

JWT mode can protect the default view and each namespace independently. Copy [config.example.toml](config.example.toml), then run `http-relay-auth secret` to generate a secret. A complete configuration looks like this:

```toml
[web]
max_transactions_per_namespace = 100

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

`max_transactions_per_namespace` applies independently to each namespace; the default view without a namespace is another independent bucket. Only the oldest records in the bucket that exceeds its limit are evicted. It can be overridden temporarily:

```bash
WEB_MAX_TRANSACTIONS_PER_NAMESPACE=500 http-relay --config ./config.toml --web
```

The secret must be unpadded Base64URL for at least 32 random bytes. Run `chmod 600 http-relay.toml` when embedding it; using `WEB_AUTH_JWT_SECRET` is preferable in deployments. JWT mode cannot be combined with `WEB_AUTH_KEY`.

Start Web mode and create an offline management token:

```bash
http-relay --config ./http-relay.toml --web
http-relay-auth issue --config ./http-relay.toml --admin
```

Paste the management token at `/login` to enter `/admin/`. The page shows record, last-activity, and SSE-subscriber counts across namespaces and can issue tokens restricted to one non-empty namespace. It cannot issue management tokens; always create those offline with `http-relay-auth issue --admin`.

Restricted tokens can also be issued and inspected offline:

```bash
http-relay-auth issue --config ./http-relay.toml --namespace team-a --ttl 24h
http-relay-auth issue --config ./http-relay.toml --namespace team-a --permanent
printf '%s' "$TOKEN" | http-relay-auth inspect --config ./http-relay.toml -
```

`--permanent` requires `allow_permanent_tokens = true` and creates a JWT without `exp`. Browser cookie cleanup can still require logging in again. The first version has no per-token revocation: rotate the secret and restart to invalidate every old JWT, then create a new management token offline and reissue restricted tokens. Treat management tokens like passwords; never put them in URLs, logs, or shell history.

JWT protects only Web pages, SSE, queries, and Clear operations. It does not authenticate writes to the Relay port. Restrict that port at the network or reverse-proxy layer when exposed beyond localhost.

Docker Compose can mount the full TOML as a Docker Secret:

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

Alternatively, mount the secret-free template and inject the secret via environment:

```yaml
environment:
  HTTP_RELAY_CONFIG: /etc/http-relay/http-relay.toml
  WEB_AUTH_JWT_SECRET: "${WEB_AUTH_JWT_SECRET}"
volumes:
  - ./config.example.toml:/etc/http-relay/http-relay.toml:ro
```

### Response preview lab

When developing preview plugins, start the local workbench without connecting it to proxy traffic:

```bash
go run ./cmd/preview-lab
```

It listens at `http://127.0.0.1:8091` by default. The page includes editable JSON, HTML, SSE, OpenAI streaming, text, and binary fixtures with instant Preview/Raw switching. Use `-listen` to change the address. The lab is development-only and is not included in the production Web UI assets.

When HTTPS is terminated by Nginx, forward these headers and enable `trust_forwarded_headers` or `--web-trust-forwarded-headers` only when that proxy is trusted:

```nginx
proxy_set_header X-Forwarded-Proto $scheme;
proxy_set_header X-Forwarded-Host $host;
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
`Authorization`, `Proxy-Authorization`, `Cookie`, `X-Api-Key`, `X-Auth-Token`, `X-Relay-Proxy`.

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
- `req.namespace`, `req.rewriteProfile`, and `req.originalPath` are read-only
  route context strings. They are identical in `onRequest` and `onResponse`.

Named rewrite profiles can bind different scripts to different request paths
without changing namespace grouping. Configure them in TOML (relative script
paths are resolved from the configuration file directory):

```toml
[rewrite.profiles.openai]
script = "./examples/rewrite.openai.js"
timeout = "500ms"
reload = "watch"

[rewrite.profiles.mock]
script = "./examples/rewrite.mock.js"
# timeout/reload omitted: inherit --script-timeout/--script-reload
```

Then select a profile with a literal `@` path segment:

```bash
curl "http://127.0.0.1:7080/@openai/https://example.com"
curl "http://127.0.0.1:7080/team-a/@mock/https://example.com/healthz"
```

`--script` remains the default script for requests without `@profile`. A named
profile runs alone and is never combined with that default. Unknown profiles
return `404`; they cannot reference arbitrary file paths. Profile selection is
available only in regular mode in this version—reverse mode forwards `@profile`
as an ordinary upstream path segment. Profiles select rewrite behavior only;
they do not authenticate writes to the Relay port.

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

### Per-request proxy

Send the `X-Relay-Proxy` header to choose the upstream proxy for a single
request, overriding the environment configuration. This lets one relay instance
route different requests through different proxies (for example, rotating
providers). The header is consumed by the relay and is never forwarded to the
target.

- Value is a proxy URL: `http`, `https`, `socks5`, or `socks5h`.
- The special value `direct` forces a direct connection (no proxy).
- When the header is absent, the environment proxy configuration applies.

```bash
# via this proxy, just for this request
curl -H 'X-Relay-Proxy: http://user:pass@proxy.example:3128' \
  http://127.0.0.1:8080/https://api.ipify.org?format=json

# force a direct connection, ignoring env proxy
curl -H 'X-Relay-Proxy: direct' \
  http://127.0.0.1:8080/https://api.ipify.org?format=json
```

## Route Rule

Default `regular` mode supports these four route shapes:

- `/{absolute-url}`
- `/{namespace}/{absolute-url}`
- `/@{profile}/{absolute-url}`
- `/{namespace}/@{profile}/{absolute-url}`

For example:

- `http://127.0.0.1:7080/https://example.com`
- `http://127.0.0.1:7080/http://httpbin.org/post`

An optional single-segment namespace can group traffic in the Web UI without changing the upstream target:

```bash
curl -i "http://127.0.0.1:7080/team-a/https://example.com"
```

With `--web`, open `http://127.0.0.1:7090/namespace/team-a/` to see only `team-a` traffic. The root Web URL shows only requests without a namespace. Namespaces may contain letters, digits, dots, underscores, and hyphens, are limited to 64 characters, and must start with a letter or digit. Old Web paths such as `/team-a/` are not supported or redirected. Without JWT they only group traffic; JWT mode makes Web reads and Clear operations an authorization boundary. Reverse mode treats the entire path as an upstream path and does not parse namespaces.

Target URL must include `http://` or `https://`.

`reverse:<url>` mode joins the incoming path and query onto a fixed upstream:

```bash
http-relay --mode reverse:https://api.example.com/base
curl "http://127.0.0.1:7080/v1/users?q=go"
```

The target is `https://api.example.com/base/v1/users?q=go`.

## Error Codes

- `400`: missing or invalid target URL
- `502`: upstream connection failure or timeout
- `500`: internal server error
