# 安全说明

http-relay 可以访问由客户端指定的上游地址。部署前应明确 Relay 端口和 Web 端口的信任边界。

## 私网目标保护

在默认的 `regular` 模式下，http-relay 会拒绝以下目标：

- loopback、本地和私网地址
- link-local、multicast 和 CGNAT 地址
- 云元数据地址
- DNS 解析结果命中以上范围的域名
- 重定向过程中命中以上范围的新目标

因此下面的请求默认会失败：

```text
http://127.0.0.1:7080/http://127.0.0.1:8080/
```

只有在受信任的内网环境确实需要访问私网上游时，才启用：

```bash
http-relay --allow-private-targets
```

::: warning
不要在公网开放匿名 Relay 的同时启用 `--allow-private-targets`。这会显著增加 SSRF 和内网探测风险。
:::

## Web 认证不保护 Relay 写入

`WEB_AUTH_KEY` 和 namespace JWT 保护的是 Web 页面、SSE、查询与清理接口。它们不限制客户端向 Relay 端口发送请求。

公网部署时，应使用防火墙、私有网络或前置反向代理单独限制 Relay 端口。

## 密钥管理

- JWT Secret 应通过 `http-relay-auth secret` 生成。
- 生产环境优先通过 `WEB_AUTH_JWT_SECRET` 注入。
- 如果 Secret 写在 TOML 中，将文件权限设置为 `0600`。
- 管理 Token 不应进入 URL、日志、Git 仓库或 shell history。
- 当前不支持单 Token 吊销；轮换 Secret 会使全部旧 JWT 失效。

## 外部 HTTP API

JavaScript 的 `relay.http.request()` 默认关闭。启用后仍应使用精确的 `allowed_origins` 白名单，并保持 `allow_private_networks = false`，除非有明确的受信任内网调用需求。
