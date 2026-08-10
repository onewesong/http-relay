# 脚本调用外部 HTTP API

Hook 可以通过同步的 `relay.http.request(options)` 调用受控外部 API，例如读取动态配置或进行鉴权。该能力默认关闭。

## 开启能力

```toml
[rewrite.http]
enabled = true
allowed_origins = ["https://config.example.com"]
timeout = "800ms"
max_timeout = "1s"
max_request_body_bytes = 1048576
max_response_body_bytes = 1048576
max_calls_per_hook = 3
follow_redirects = false
allow_private_networks = false

[rewrite.profiles.external-config]
script = "./plugins/examples/rewrite.external-config.js"
timeout = "1500ms"
reload = "watch"
```

`allowed_origins` 是 scheme、hostname 和有效端口组成的精确白名单，不支持路径、通配符、CIDR 或正则表达式。

## 发起请求

```js
function onRequest(req) {
  if (!relay.http.enabled) return;

  try {
    var response = relay.http.request({
      url: "https://config.example.com/v1/features",
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ namespace: req.namespace }),
      timeoutMs: 300,
    });

    if (response.status === 200) {
      req.headers["X-Feature"] = JSON.parse(response.body).enabled ? "1" : "0";
    }
  } catch (error) {
    console.warn("external API failed:", error.message);
  }
}
```

返回对象包含 `status`、`headers`、`body` 和最终 `url`。HTTP `4xx/5xx` 正常返回；网络、超时、白名单、私网、重定向和大小限制错误会抛出可捕获异常。

## 安全与超时

- 外部 Client 不继承原请求的 Authorization、Cookie 或代理环境变量。
- DNS 解析结果与实际连接地址会一起校验，降低 DNS rebinding 风险。
- Hook timeout 覆盖外部请求和后续 JavaScript 执行。
- 有效请求时间取 `timeoutMs`、`max_timeout` 和 Hook 剩余时间中的最小值。
- 即使允许私网，目标仍必须命中精确 origin 白名单。
