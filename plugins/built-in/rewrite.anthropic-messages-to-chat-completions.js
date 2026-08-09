// Compatibility bridge for Anthropic Messages clients and Chat Completions
// upstreams. Supports text, base64 images, custom function tools and SSE.

function isMessages(url) { return /\/v1\/messages(?:\?|$)/.test(url); }

function fail(message) {
  return { status: 400, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ type: "error", error: { type: "invalid_request_error", message: message } }) };
}

function text(value, label) {
  if (typeof value === "string") return value;
  if (!Array.isArray(value)) return "";
  var result = "";
  for (var i = 0; i < value.length; i++) {
    var block = value[i] || {};
    if (block.type !== "text" || typeof block.text !== "string") throw new Error(label + " only supports text blocks");
    result += block.text;
  }
  return result;
}

function chatContent(content) {
  if (typeof content === "string") return { content: content, tools: [], results: [] };
  if (!Array.isArray(content)) throw new Error("message content must be a string or an array");
  var parts = [], tools = [], results = [];
  for (var i = 0; i < content.length; i++) {
    var block = content[i] || {};
    if (block.type === "text" && typeof block.text === "string") parts.push({ type: "text", text: block.text });
    else if (block.type === "image" && block.source && block.source.type === "base64" && block.source.media_type && block.source.data) {
      parts.push({ type: "image_url", image_url: { url: "data:" + block.source.media_type + ";base64," + block.source.data } });
    } else if (block.type === "tool_use" && block.id && block.name) {
      tools.push({ id: block.id, type: "function", function: { name: block.name, arguments: JSON.stringify(block.input || {}) } });
    } else if (block.type === "tool_result" && block.tool_use_id) {
      results.push({ role: "tool", tool_call_id: block.tool_use_id, content: text(block.content, "tool_result") });
    } else throw new Error("unsupported Anthropic content block type: " + (block.type || "unknown"));
  }
  return { content: parts, tools: tools, results: results };
}

function chatTools(tools) {
  if (!Array.isArray(tools)) return undefined;
  var result = [];
  for (var i = 0; i < tools.length; i++) {
    var tool = tools[i] || {};
    if (!tool.name || !tool.input_schema) throw new Error("tools require name and input_schema");
    result.push({ type: "function", function: { name: tool.name, description: tool.description, parameters: tool.input_schema } });
  }
  return result;
}

function toolChoice(choice) {
  if (!choice) return undefined;
  if (choice.type === "auto") return "auto";
  if (choice.type === "any") return "required";
  if (choice.type === "tool" && choice.name) return { type: "function", function: { name: choice.name } };
  throw new Error("unsupported tool_choice");
}

function onRequest(req) {
  if (!isMessages(req.url) || req.method !== "POST") return;
  var input;
  try { input = JSON.parse(req.body); } catch (error) { return fail("messages body must be valid JSON"); }
  if (Array.isArray(input.stop_sequences) && input.stop_sequences.length) return fail("stop_sequences is not supported by this compatibility plugin");
  try {
    var messages = [];
    var system = text(input.system, "system");
    if (system) messages.push({ role: "system", content: system });
    var source = Array.isArray(input.messages) ? input.messages : [];
    for (var i = 0; i < source.length; i++) {
      var item = source[i] || {}, converted = chatContent(item.content);
      if (converted.content !== "" && (!Array.isArray(converted.content) || converted.content.length)) {
        var message = { role: item.role || "user", content: converted.content };
        if (converted.tools.length) message.tool_calls = converted.tools;
        messages.push(message);
      } else if (converted.tools.length) {
        messages.push({ role: "assistant", content: null, tool_calls: converted.tools });
      }
      for (var j = 0; j < converted.results.length; j++) messages.push(converted.results[j]);
    }
    var body = { model: input.model, messages: messages, stream: input.stream === true, max_tokens: input.max_tokens, temperature: input.temperature, top_p: input.top_p, tools: chatTools(input.tools), tool_choice: toolChoice(input.tool_choice) };
    if (input.stream === true) body.stream_options = { include_usage: true };
    req.streamResponse = input.stream === true;
    if (req.headers["X-Api-Key"]) req.headers["Authorization"] = "Bearer " + req.headers["X-Api-Key"];
    delete req.headers["X-Api-Key"];
    delete req.headers["Anthropic-Version"];
    delete req.headers["Anthropic-Beta"];
    req.url = req.url.replace("/v1/messages", "/v1/chat/completions");
    req.body = JSON.stringify(body);
  } catch (error) { return fail(error.message); }
}

function usage(value) { return { input_tokens: value ? (value.prompt_tokens || 0) : 0, output_tokens: value ? (value.completion_tokens || 0) : 0 }; }
function stopReason(finish, hasTools) { return hasTools || finish === "tool_calls" ? "tool_use" : (finish === "length" ? "max_tokens" : "end_turn"); }

function onResponse(resp, req) {
  if (!isMessages(req.originalPath)) return;
  var value;
  try { value = JSON.parse(resp.body || "{}"); } catch (error) { value = {}; }
  if (resp.status < 200 || resp.status >= 300) {
    var detail = value.error || value;
    resp.body = JSON.stringify({ type: "error", error: { type: detail.type || "api_error", message: detail.message || "upstream Chat Completions request failed" } });
    resp.headers["Content-Type"] = "application/json";
    return;
  }
  var choice = (value.choices || [])[0] || {}, message = choice.message || {}, content = [];
  if (message.content) content.push({ type: "text", text: message.content });
  var calls = Array.isArray(message.tool_calls) ? message.tool_calls : [];
  for (var i = 0; i < calls.length; i++) {
    var call = calls[i] || {}, fn = call.function || {}, args = {};
    try { args = JSON.parse(fn.arguments || "{}"); } catch (error) {}
    content.push({ type: "tool_use", id: call.id, name: fn.name, input: args });
  }
  resp.body = JSON.stringify({ id: value.id, type: "message", role: "assistant", model: value.model, content: content, stop_reason: stopReason(choice.finish_reason, calls.length > 0), stop_sequence: null, usage: usage(value.usage) });
  resp.headers["Content-Type"] = "application/json";
}

function event(data) { return { data: JSON.stringify(data) }; }
function begin(state) {
  if (state.started) return [];
  state.started = true;
  return [event({ type: "message_start", message: { id: state.id || "msg_relay", type: "message", role: "assistant", model: state.model, content: [], stop_reason: null, stop_sequence: null, usage: state.usage } })];
}
function open(state, index, block) { state.blocks[index] = { index: state.nextIndex++, open: true, tool: block.type === "tool_use" }; return event({ type: "content_block_start", index: state.blocks[index].index, content_block: block }); }
function close(state, index) { var b = state.blocks[index]; if (!b || !b.open) return null; b.open = false; return event({ type: "content_block_stop", index: b.index }); }

function onResponseStart(resp, req) {
  if (!isMessages(req.originalPath)) throw new Error("compatibility script only supports /v1/messages");
  resp.headers["Content-Type"] = "text/event-stream";
  delete resp.headers["Content-Length"];
  return { started: false, completed: false, id: null, model: null, usage: { input_tokens: 0, output_tokens: 0 }, blocks: {}, nextIndex: 0, hasTools: false };
}

function onResponseEvent(incoming, state, req) {
  if (incoming.data === "[DONE]") return null;
  var value;
  try { value = JSON.parse(incoming.data); } catch (error) { return null; }
  if (value.id) state.id = value.id;
  if (value.model) state.model = value.model;
  if (value.usage) state.usage = usage(value.usage);
  var choice = (value.choices || [])[0] || {}, delta = choice.delta || {}, out = begin(state);
  if (delta.content) {
    if (!state.blocks.text) out.push(open(state, "text", { type: "text", text: "" }));
    out.push(event({ type: "content_block_delta", index: state.blocks.text.index, delta: { type: "text_delta", text: delta.content } }));
  }
  var calls = Array.isArray(delta.tool_calls) ? delta.tool_calls : [];
  for (var i = 0; i < calls.length; i++) {
    var call = calls[i] || {}, key = "tool-" + (call.index === undefined ? i : call.index), fn = call.function || {};
    if (!state.blocks[key]) {
      state.hasTools = true;
      out.push(open(state, key, { type: "tool_use", id: call.id || key, name: fn.name || "", input: {} }));
    }
    if (fn.arguments) out.push(event({ type: "content_block_delta", index: state.blocks[key].index, delta: { type: "input_json_delta", partial_json: fn.arguments } }));
  }
  if (choice.finish_reason) {
    for (var keyName in state.blocks) { var stopped = close(state, keyName); if (stopped) out.push(stopped); }
    state.completed = true;
    out.push(event({ type: "message_delta", delta: { stop_reason: stopReason(choice.finish_reason, state.hasTools), stop_sequence: null }, usage: { output_tokens: state.usage.output_tokens } }));
    out.push(event({ type: "message_stop" }));
  }
  return out;
}

function onResponseEnd(state, req) {
  if (state.completed) return null;
  var out = begin(state);
  for (var key in state.blocks) { var stopped = close(state, key); if (stopped) out.push(stopped); }
  out.push(event({ type: "message_stop" }));
  return out;
}
