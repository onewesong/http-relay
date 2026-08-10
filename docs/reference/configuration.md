# 配置文件

http-relay 使用严格 TOML 解析。未知字段、错误类型、非法枚举或不满足约束的值都会使程序在启动阶段失败。

## 加载优先级

1. `--config <path>`
2. `HTTP_RELAY_CONFIG`
3. 未提供配置，使用内置默认值

程序不会自动读取当前目录中的 `config.toml`。

```bash
http-relay --config ./http-relay.toml --web
HTTP_RELAY_CONFIG=./http-relay.toml http-relay --web
```

配置中的相对脚本路径以配置文件所在目录为基准。

## 配置结构

```toml
[rewrite]
# SSE 事件级 Hook 的资源限制

[rewrite.http]
# JavaScript 外部 HTTP API

[rewrite.profiles.<name>]
# 路径绑定的具名改写 Profile

[web]
# Web UI 记录设置

[web.auth]
# Web UI JWT 认证

[web.auth.namespaces]
# namespace 的公开/受保护策略
```

## Rewrite 资源限制

```toml
[rewrite]
max_sse_event_bytes = 1048576
max_sse_events_per_response = 100000
```

这些限制只影响定义了 `onResponseEvent` 的 SSE 事件级脚本。

## Web 记录设置

```toml
[web]
max_transactions_per_namespace = 100
```

默认视图和每个 namespace 独立保留最多 100 条记录，超过限制时淘汰该分组最旧的记录。

## 完整示例

复制仓库提供的 [`config.example.toml`](https://github.com/onewesong/http-relay/blob/main/config.example.toml) 作为起点。各功能的详细配置分别参见：

- [Rewrite Profile](/scripting/profiles)
- [脚本调用外部 HTTP API](/scripting/external-http)
- [Web UI 与认证](/features/web-ui)

仓库还保留了一份较完整的[配置字段说明](https://github.com/onewesong/http-relay/blob/main/docs/configuration.zh-CN.md)，用于查看全部约束和安全边界。
