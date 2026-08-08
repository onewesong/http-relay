// Example profile for API traffic. Route with /@openai/{absolute-url} or
// /{namespace}/@openai/{absolute-url}.
function onRequest(req) {

  // Enable web search for Responses requests unless the caller already added it.
  if (/\/v1\/responses(?:\?|$)/.test(req.url) && req.body) {
    try {
      var data = JSON.parse(req.body);
      if (!Array.isArray(data.tools)) data.tools = [];

      var hasWebSearch = false;
      for (var i = 0; i < data.tools.length; i++) {
        if (data.tools[i] && data.tools[i].type === "web_search") {
          hasWebSearch = true;
          break;
        }
      }

      if (!hasWebSearch) {
        data.tools.push({ type: "web_search" });
        req.body = JSON.stringify(data);
      }
    } catch (error) {
      console.warn("request body is not valid JSON:", error.message);
    }
  }
}

function onResponse(resp, req) {
  resp.headers["X-Relay-Profile"] = req.rewriteProfile;
}
