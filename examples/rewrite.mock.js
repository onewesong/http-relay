// Return a local response for health checks; other requests continue upstream.
function onRequest(req) {
  if (req.url.indexOf("/healthz") >= 0) {
    return {
      status: 200,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ok: true, namespace: req.namespace || "default" }),
    };
  }
}
