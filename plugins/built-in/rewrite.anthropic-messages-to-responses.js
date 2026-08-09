// Compatibility bridge for Anthropic Messages clients and Responses-only
// upstreams. It supports text, base64 images, custom function tools and SSE.

function isMessages(url) { return /\/v1\/messages(?:\?|$)/.test(url); }

function anthropicError(message) {
  return {
    status: 400,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ type: "error", error: { type: "invalid_request_error", message: message } })
  };
}

function textBlocks(value, label) {
  if (typeof value === "string") return value;
  if (!Array.isArray(value)) return "";
  var text = "";
  for (var i = 0; i < value.length; i++) {
    var block = value[i] || {};
    if (block.type !== "text" || typeof block.text !== "string") throw new Error(label + " only supports text blocks");
    text += block.text;
  }
  return text;
}

function inputBlocks(content) {
  if (typeof content === "string") return { content: [{ type: "input_text", text: content }], calls: [], results: [] };
  if (!Array.isArray(content)) throw new Error("message content must be a string or an array");
  var result = { content: [], calls: [], results: [] };
  for (var i = 0; i < content.length; i++) {
    var block = content[i] || {};
    if (block.type === "text" && typeof block.text === "string") {
      result.content.push({ type: "input_text", text: block.text });
    } else if (block.type === "image" && block.source && block.source.type === "base64" && block.source.media_type && block.source.data) {
      result.content.push({ type: "input_image", image_url: "data:" + block.source.media_type + ";base64," + block.source.data });
    } else if (block.type === "tool_use" && block.id && block.name) {
      result.calls.push({ type: "function_call", call_id: block.id, name: block.name, arguments: JSON.stringify(block.input || {}) });
    } else if (block.type === "tool_result" && block.tool_use_id) {
      result.results.push({ type: "function_call_output", call_id: block.tool_use_id, output: textBlocks(block.content, "tool_result") });
    } else {
      throw new Error("unsupported Anthropic content block type: " + (block.type || "unknown"));
    }
  }
  return result;
}

function responseTools(tools) {
  if (!Array.isArray(tools)) return undefined;
  var result = [];
  for (var i = 0; i < tools.length; i++) {
    var tool = tools[i] || {};
    if (!tool.name || !tool.input_schema) throw new Error("tools require name and input_schema");
    result.push({ type: "function", name: tool.name, description: tool.description, parameters: tool.input_schema });
  }
  return result;
}

function responseToolChoice(choice) {
  if (!choice) return undefined;
  if (choice.type === "auto") return "auto";
  if (choice.type === "any") return "required";
  if (choice.type === "tool" && choice.name) return { type: "function", name: choice.name };
  throw new Error("unsupported tool_choice");
}

function onRequest(req) {
  if (!isMessages(req.url) || req.method !== "POST") return;
  var message;
  try { message = JSON.parse(req.body); } catch (error) { return anthropicError("messages body must be valid JSON"); }
  if (Array.isArray(message.stop_sequences) && message.stop_sequences.length) return anthropicError("stop_sequences is not supported by this compatibility plugin");

  try {
    var input = [];
    var messages = Array.isArray(message.messages) ? message.messages : [];
    for (var i = 0; i < messages.length; i++) {
      var entry = messages[i] || {};
      var converted = inputBlocks(entry.content);
      if (converted.content.length) input.push({ role: entry.role || "user", content: converted.content });
      for (var j = 0; j < converted.calls.length; j++) input.push(converted.calls[j]);
      for (var k = 0; k < converted.results.length; k++) input.push(converted.results[k]);
    }
    var body = {
      model: message.model,
      input: input,
      instructions: textBlocks(message.system, "system"),
      stream: message.stream === true,
      max_output_tokens: message.max_tokens,
      temperature: message.temperature,
      top_p: message.top_p,
      tools: responseTools(message.tools),
      tool_choice: responseToolChoice(message.tool_choice)
    };
    req.streamResponse = message.stream === true;
    if (req.headers["X-Api-Key"]) req.headers["Authorization"] = "Bearer " + req.headers["X-Api-Key"];
    delete req.headers["X-Api-Key"];
    delete req.headers["Anthropic-Version"];
    delete req.headers["Anthropic-Beta"];
    req.url = req.url.replace("/v1/messages", "/v1/responses");
    req.body = JSON.stringify(body);
  } catch (error) {
    return anthropicError(error.message);
  }
}

function anthropicStopReason(response, hasTools) {
  if (hasTools) return "tool_use";
  if (response.incomplete_details && response.incomplete_details.reason === "max_output_tokens") return "max_tokens";
  return "end_turn";
}

function responseUsage(usage) {
  return { input_tokens: usage ? (usage.input_tokens || 0) : 0, output_tokens: usage ? (usage.output_tokens || 0) : 0 };
}

function onResponse(resp, req) {
  if (!isMessages(req.originalPath)) return;
  var response;
  try { response = JSON.parse(resp.body || "{}"); } catch (error) { response = {}; }
  if (resp.status < 200 || resp.status >= 300) {
    var detail = response.error || response;
    resp.body = JSON.stringify({ type: "error", error: { type: detail.type || "api_error", message: detail.message || "upstream Responses request failed" } });
    resp.headers["Content-Type"] = "application/json";
    return;
  }
  var content = [], output = Array.isArray(response.output) ? response.output : [];
  var hasTools = false;
  for (var i = 0; i < output.length; i++) {
    var item = output[i] || {};
    if (item.type === "message") {
      var parts = Array.isArray(item.content) ? item.content : [];
      for (var j = 0; j < parts.length; j++) {
        if (parts[j].type === "output_text") content.push({ type: "text", text: parts[j].text || "" });
      }
    } else if (item.type === "function_call") {
      hasTools = true;
      var input = {};
      try { input = JSON.parse(item.arguments || "{}"); } catch (error) {}
      content.push({ type: "tool_use", id: item.call_id || item.id, name: item.name, input: input });
    }
  }
  resp.body = JSON.stringify({ id: response.id, type: "message", role: "assistant", model: response.model, content: content, stop_reason: anthropicStopReason(response, hasTools), stop_sequence: null, usage: responseUsage(response.usage) });
  resp.headers["Content-Type"] = "application/json";
}

function sse(data) { return { data: JSON.stringify(data) }; }

function streamStart(state) {
  if (state.started) return [];
  state.started = true;
  return [sse({ type: "message_start", message: { id: state.id || "msg_relay", type: "message", role: "assistant", model: state.model, content: [], stop_reason: null, stop_sequence: null, usage: state.usage } })];
}

function addBlock(state, itemID, block) {
  var index = state.nextIndex++;
  state.blocks[itemID] = { index: index, open: true, tool: block.type === "tool_use" };
  return sse({ type: "content_block_start", index: index, content_block: block });
}

function closeBlock(state, itemID) {
  var block = state.blocks[itemID];
  if (!block || !block.open) return null;
  block.open = false;
  return sse({ type: "content_block_stop", index: block.index });
}

function onResponseStart(resp, req) {
  if (!isMessages(req.originalPath)) throw new Error("compatibility script only supports /v1/messages");
  resp.headers["Content-Type"] = "text/event-stream";
  delete resp.headers["Content-Length"];
  return { started: false, completed: false, id: null, model: null, usage: { input_tokens: 0, output_tokens: 0 }, blocks: {}, nextIndex: 0, hasTools: false };
}

function onResponseEvent(event, state, req) {
  var value;
  try { value = JSON.parse(event.data); } catch (error) { return null; }
  var response = value.response || value;
  if (response.id) state.id = response.id;
  if (response.model) state.model = response.model;
  if (response.usage) state.usage = responseUsage(response.usage);
  var out = [];
  if (event.event === "response.created") return streamStart(state);
  if (event.event === "response.output_item.added" && value.item && value.item.type === "function_call") {
    out = streamStart(state); state.hasTools = true;
    out.push(addBlock(state, value.item.id, { type: "tool_use", id: value.item.call_id || value.item.id, name: value.item.name, input: {} }));
  } else if (event.event === "response.content_part.added" && value.part && value.part.type === "output_text") {
    out = streamStart(state); out.push(addBlock(state, value.item_id || ("text-" + state.nextIndex), { type: "text", text: "" }));
  } else if (event.event === "response.output_text.delta") {
    out = streamStart(state); var textID = value.item_id || "text-default";
    if (!state.blocks[textID]) out.push(addBlock(state, textID, { type: "text", text: "" }));
    out.push(sse({ type: "content_block_delta", index: state.blocks[textID].index, delta: { type: "text_delta", text: value.delta || "" } }));
  } else if (event.event === "response.function_call_arguments.delta") {
    out = streamStart(state); var call = state.blocks[value.item_id];
    if (!call) return out;
    out.push(sse({ type: "content_block_delta", index: call.index, delta: { type: "input_json_delta", partial_json: value.delta || "" } }));
  } else if (event.event === "response.output_item.done") {
    var stopped = closeBlock(state, value.item && value.item.id ? value.item.id : value.item_id); if (stopped) out.push(stopped);
  } else if (event.event === "response.completed") {
    out = streamStart(state);
    for (var id in state.blocks) { var end = closeBlock(state, id); if (end) out.push(end); }
    state.completed = true;
    out.push(sse({ type: "message_delta", delta: { stop_reason: anthropicStopReason(response, state.hasTools), stop_sequence: null }, usage: { output_tokens: state.usage.output_tokens } }));
    out.push(sse({ type: "message_stop" }));
  } else if (event.event === "error" || event.event === "response.failed") {
    out.push({ event: "error", data: JSON.stringify({ type: "error", error: value.error || { type: "api_error", message: "upstream Responses stream failed" } }) });
  }
  return out;
}

function onResponseEnd(state, req) {
  if (state.completed) return null;
  var out = streamStart(state);
  for (var id in state.blocks) { var end = closeBlock(state, id); if (end) out.push(end); }
  out.push(sse({ type: "message_stop" }));
  return out;
}
