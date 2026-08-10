# 转发模式与路由

## Regular 模式

默认模式从路径中读取绝对 URL：

```bash
http-relay --mode regular
curl "http://127.0.0.1:7080/https://example.com/api/users?page=1"
```

支持四种路径结构：

```text
/{absolute-url}
/{namespace}/{absolute-url}
/@{profile}/{absolute-url}
/{namespace}/@{profile}/{absolute-url}
```

例如：

```bash
curl "http://127.0.0.1:7080/team-a/https://example.com"
curl "http://127.0.0.1:7080/@openai/https://api.example.com/v1/responses"
curl "http://127.0.0.1:7080/team-a/@mock/https://example.com/healthz"
```

`namespace` 只用于 Web UI 流量分组和访问控制，不会发送给上游。名称必须以字母或数字开头，只能包含字母、数字、点、下划线和连字符，最长 64 个字符。

## Reverse 模式

将 Relay 固定到一个上游：

```bash
http-relay --mode reverse:https://api.example.com/base
curl "http://127.0.0.1:7080/v1/users?q=go"
```

最终目标为：

```text
https://api.example.com/base/v1/users?q=go
```

Reverse 模式不会解析 namespace 或 `@profile`，收到的路径会完整拼接到固定上游。

## 请求头改写

追加一个可重复的请求头：

```bash
http-relay --add-header "X-Debug: 1"
```

设置或覆盖请求头：

```bash
http-relay --modify-header "User-Agent: http-relay"
```

复杂条件或 Body 改写应使用 [JavaScript Rewrite](/scripting/getting-started)。

## 使用上游代理

普通上游请求支持标准代理环境变量：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 http-relay
ALL_PROXY=socks5://127.0.0.1:1080 http-relay
HTTPS_PROXY=http://127.0.0.1:7890 NO_PROXY=example.com http-relay
```

优先级为 `ALL_PROXY` 高于 `HTTP_PROXY` / `HTTPS_PROXY`；`NO_PROXY` 命中后直连。
