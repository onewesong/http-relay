# 流量观察

http-relay 提供普通终端转储、交互式 TUI 和 Web UI 三种观察方式。

## 终端转储

```bash
http-relay --dump
http-relay -w
```

限制输出范围：

```bash
http-relay --dump --dump-scope req
http-relay --dump --dump-scope resp
http-relay --dump --dump-scope req,resp
```

也可以使用兼容环境变量 `WIRE_SCOPE`。

对认证相关请求头脱敏：

```bash
http-relay --dump --mask-auth
```

脱敏范围包含 `Authorization`、`Proxy-Authorization`、`Cookie`、`X-Api-Key` 和 `X-Auth-Token`。

## TUI

```bash
http-relay --tui
```

- 方向键或 `j` / `k` 选择请求
- `Enter` 展开头部与正文
- `q` 退出

TUI 需要交互式终端，并与 `--web` 互斥。

## Web UI

```bash
http-relay --web --web-listen 127.0.0.1:7090
```

Web UI 通过 SSE 实时接收流量，支持 Raw 与 Preview 切换。Preview 可以格式化 JSON、在沙箱中预览 HTML，并合并 SSE/OpenAI 流式事件。

Conversations 视图可以通过显式会话 ID、`previous_response_id` 或完整消息历史关联连续 OpenAI 请求，并跳回原始请求。

按 namespace 查看：

```text
http://127.0.0.1:7090/
http://127.0.0.1:7090/namespace/team-a/
```
