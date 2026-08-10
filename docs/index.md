---
layout: home

hero:
  name: http-relay
  text: 让 HTTP 流量可转发、可观察、可编程
  tagline: 一个轻量的 Go HTTP Relay，支持正向转发、固定上游反向代理、实时 Web UI 和 JavaScript 流量改写。
  image:
    src: /logo.svg
    alt: http-relay
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/getting-started
    - theme: alt
      text: 查看 GitHub
      link: https://github.com/onewesong/http-relay

features:
  - icon: ↗️
    title: 灵活转发
    details: 将绝对 URL 写进请求路径，或绑定固定上游作为反向代理。
  - icon: 👁️
    title: 实时观察
    details: 通过终端转储、交互式 TUI 或 Web UI 查看请求、响应和连续对话。
  - icon: ⚡
    title: JavaScript 改写
    details: 在请求与响应 Hook 中改写 URL、Header、Body、状态码，或者直接返回本地 Mock。
  - icon: 🔐
    title: 安全边界
    details: 默认拦截私网目标，并为 Web UI 提供密钥或 namespace JWT 认证。
---

## 一分钟体验

安装并启动服务：

```bash
curl -fsSL https://raw.githubusercontent.com/onewesong/http-relay/main/install.sh | sh
http-relay
```

在另一个终端发起请求：

```bash
curl -i "http://127.0.0.1:7080/https://example.com"
```

需要查看实时流量时，加上 Web UI：

```bash
http-relay --web
```

然后访问 [http://127.0.0.1:7090](http://127.0.0.1:7090)。

::: tip 下一步
阅读[安装与快速开始](/guide/getting-started)，或直接了解 [JavaScript Rewrite](/scripting/getting-started)。
:::
