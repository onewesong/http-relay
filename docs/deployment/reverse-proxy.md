# 反向代理 Web UI

Web UI 可以放在 Nginx、Caddy 或其他 HTTPS 反向代理之后。Relay 端口通常只对可信客户端或内网开放。

## Nginx 示例

```nginx
server {
    listen 443 ssl;
    server_name relay.example.com;

    location / {
        proxy_pass http://127.0.0.1:7090;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Host $host;
        proxy_set_header Connection "";
        proxy_buffering off;
    }
}
```

启动时显式信任转发头：

```bash
http-relay --web --web-trust-forwarded-headers
```

或在 TOML 中配置：

```toml
[web.auth]
trust_forwarded_headers = true
```

::: warning
只有在 Web 端口无法被客户端绕过、且前置代理会覆盖这些请求头时，才能信任 `X-Forwarded-Proto` 和 `X-Forwarded-Host`。
:::

SSE 需要长连接。应关闭代理缓冲，并确保代理读取超时足够长。
