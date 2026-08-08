// http-relay 自定义改写脚本示例
//
// 用法:
//   http-relay --script ./plugins/examples/relay.example.js
//   http-relay --script ./plugins/examples/relay.example.js --script-reload watch   // 改文件即时生效
//
// 脚本可选地导出两个钩子函数（都可以省略）:
//   onRequest(req)        改写发往上游的请求；返回对象可短路（不打上游，直接回客户端）
//   onResponse(resp, req) 改写返回给客户端的响应
//
// 对象字段（直接原地修改即生效）:
//   req.method / req.url / req.host   字符串
//   req.headers / resp.headers        普通对象，键为规范化后的头名
//                                       h["X-Foo"] = "v"   新增/覆盖
//                                       delete h["X-Foo"]  删除该头
//                                       h["X-Foo"] = ""    保留该头但值为空
//   req.body / resp.body              字符串
//   resp.status                       数字
//
// console.log / info / warn / error / debug 输出到 http-relay 的 stderr（TUI 下静默）。

// 改写发往上游的请求。
function onRequest(req) {
  // 1) 注入/覆盖请求头
  req.headers["X-Trace-Id"] = "trace-" + Date.now();
  req.headers["User-Agent"] = "http-relay-script";

  // 2) 删除敏感头
  delete req.headers["Cookie"];

  // 3) 健康检查直接本地 Mock，不打上游（短路）。
  //    短路后 onResponse 仍会执行，可进一步加工这个响应。
  if (req.url.indexOf("/healthz") >= 0) {
    return {
      status: 200,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ok: true, mocked: true }),
    };
  }

  // 4) 路由重写：把老路径转发到新版本接口。
  if (req.url.indexOf("/api/v1/") >= 0) {
    req.url = req.url.replace("/api/v1/", "/api/v2/");
    console.log("rerouted to", req.url);
  }

  // 5) 改写请求体（仅对 JSON）：注入一个字段。
  var ct = req.headers["Content-Type"] || "";
  if (req.method === "POST" && ct.indexOf("json") >= 0 && req.body) {
    try {
      var data = JSON.parse(req.body);
      data.injectedByRelay = true;
      req.body = JSON.stringify(data);
    } catch (e) {
      console.warn("request body is not valid JSON:", e.message);
    }
  }

  // 6) 拦截：拒绝访问某些路径。
  if (req.url.indexOf("/admin") >= 0) {
    return { status: 403, body: "forbidden by relay\n" };
  }
}

// 改写返回给客户端的响应。
function onResponse(resp, req) {
  // 标记响应来源，方便调试。
  resp.headers["X-Proxied-By"] = "http-relay";

  // 删除上游可能泄露的内部头。
  delete resp.headers["Server"];
  delete resp.headers["X-Powered-By"];

  // 对 JSON 响应注入字段（演示读取请求上下文 req）。
  var ct = resp.headers["Content-Type"] || "";
  if (ct.indexOf("json") >= 0 && resp.body) {
    try {
      var data = JSON.parse(resp.body);
      data.relayMeta = { path: req.url, status: resp.status };
      resp.body = JSON.stringify(data);
    } catch (e) {
      console.warn("response body is not valid JSON:", e.message);
    }
  }

  // 把上游的 500 统一改写为更友好的 503（示例）。
  if (resp.status === 500) {
    resp.status = 503;
    resp.body = "service temporarily unavailable\n";
  }
}
