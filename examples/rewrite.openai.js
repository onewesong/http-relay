// Example profile for API traffic. Route with /@openai/{absolute-url} or
// /{namespace}/@openai/{absolute-url}.
function onRequest(req) {
  req.headers["X-Relay-Rewrite"] = "openai";
  req.headers["X-Relay-Namespace"] = req.namespace || "default";
}

function onResponse(resp, req) {
  resp.headers["X-Relay-Profile"] = req.rewriteProfile;
}
