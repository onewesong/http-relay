// Query a low-latency external configuration API before forwarding upstream.
// The target origin must be explicitly allowed under [rewrite.http].
function onRequest(req) {
  if (!relay.http.enabled) return;

  try {
    var response = relay.http.request({
      url: "https://config.example.com/v1/features",
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ namespace: req.namespace || "default" }),
      timeoutMs: 300,
    });
    if (response.status !== 200) return;

    var feature = JSON.parse(response.body);
    req.headers["X-Feature-Enabled"] = String(feature.enabled === true);
  } catch (error) {
    // Network and policy failures are catchable so the request can degrade
    // gracefully instead of failing the whole Relay request.
    console.warn("external config lookup failed:", error.message);
  }
}
