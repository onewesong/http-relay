// Map an upstream HTTP 429 (Too Many Requests) response to 529 for clients.
function onResponse(resp, req) {
  if (resp.status === 429) {
    resp.status = 529;
  }
}
