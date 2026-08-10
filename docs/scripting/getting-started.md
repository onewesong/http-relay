# 编写第一个改写脚本

JavaScript Rewrite 可以在请求发送前和响应返回前修改流量。运行时是同步、受超时限制的 JavaScript 环境，不提供浏览器 `fetch`、Promise 或 `async/await`。

## 基本结构

创建 `rewrite.js`：

```js
function onRequest(req) {
  req.headers["X-Proxied-By"] = "http-relay";

  if (req.url.indexOf("/healthz") >= 0) {
    return {
      status: 200,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ok: true, mocked: true }),
    };
  }
}

function onResponse(resp, req) {
  delete resp.headers["Server"];
  resp.headers["X-Original-URL"] = req.url;
}
```

加载脚本：

```bash
http-relay --script ./rewrite.js
```

## Hook 对象

`onRequest(req)` 可读写：

- `req.method`、`req.url`、`req.host`
- `req.headers`、`req.body`
- 只读的 `req.namespace`、`req.rewriteProfile`、`req.originalPath`

返回 `{ status, headers, body }` 可以短路上游请求。短路响应仍会进入 `onResponse`。

`onResponse(resp, req)` 可读写：

- `resp.status`、`resp.headers`、`resp.body`
- 同一个请求上下文 `req`

## 超时与热更新

```bash
http-relay \
  --script ./rewrite.js \
  --script-timeout 500ms \
  --script-reload watch
```

热更新模式包括：

- `watch`：默认，监听文件变化
- `poll`：轮询文件变化
- `off`：不热更新，适合不可变部署

脚本异常或超时不会无限阻塞 Relay。应避免在 Hook 中进行昂贵计算，并为外部 HTTP 调用预留足够的 Hook 时间。

完整示例可查看仓库中的 [`plugins/examples/relay.example.js`](https://github.com/onewesong/http-relay/blob/main/plugins/examples/relay.example.js)。
