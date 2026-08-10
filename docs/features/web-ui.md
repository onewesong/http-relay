# Web UI 与认证

## 无认证模式

```bash
http-relay --web
```

未设置认证配置时，任何能访问 Web 端口的用户都可以读取和清理记录。只建议用于本机或可信网络。

## 共享密钥认证

设置 `WEB_AUTH_KEY` 即可启用简单登录：

```bash
WEB_AUTH_KEY='replace-with-a-long-random-secret' http-relay --web
```

登录会话有效期为 24 小时。这种方式适合单用户或小型可信团队。

## Namespace JWT 认证

需要隔离多个 namespace 时，在 TOML 中启用 JWT：

```toml
[web]
max_transactions_per_namespace = 100

[web.auth]
mode = "jwt"
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
public-demo = false
```

生成 Secret 并通过环境变量注入：

```bash
http-relay-auth secret
WEB_AUTH_JWT_SECRET="$SECRET" http-relay --config ./http-relay.toml --web
```

离线签发管理 Token：

```bash
http-relay-auth issue --config ./http-relay.toml --admin
```

在 `/login` 粘贴管理 Token 后进入 `/admin/`，可以查看各 namespace 的记录和订阅情况，并签发仅限单一 namespace 的 Token。

命令行也可以签发和检查受限 Token：

```bash
http-relay-auth issue --config ./http-relay.toml --namespace team-a --ttl 24h
printf '%s' "$TOKEN" | http-relay-auth inspect --config ./http-relay.toml -
```

::: warning
JWT 只保护 Web 端口，不保护 Relay 端口的请求写入。请在网络层单独限制 Relay 端口。
:::
