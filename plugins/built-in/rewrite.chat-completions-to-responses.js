// Compatibility bridge for Chat Completions clients and Responses-only
// upstreams. It selects buffered JSON or SSE conversion from request.stream.

function isChatCompletions(url) { return /\/v1\/chat\/completions(?:\?|$)/.test(url); }

function textFromContent(content) {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return "";
  var text = "";
  for (var i = 0; i < content.length; i++) {
    var part = content[i] || {};
    if (part.type === "text" && typeof part.text === "string") text += part.text;
  }
  return text;
}

function responseContent(content) {
  if (typeof content === "string") return [{ type: "input_text", text: content }];
  if (!Array.isArray(content)) return [];
  var result = [];
  for (var i = 0; i < content.length; i++) {
    var part = content[i] || {};
    if (part.type === "text" && typeof part.text === "string") result.push({ type: "input_text", text: part.text });
    else if (part.type === "image_url" && part.image_url) {
      var imageURL = typeof part.image_url === "string" ? part.image_url : part.image_url.url;
      if (imageURL) result.push({ type: "input_image", image_url: imageURL });
    }
  }
  return result;
}

function responseTools(tools) {
  if (!Array.isArray(tools)) return undefined;
  var result = [];
  for (var i = 0; i < tools.length; i++) {
    var tool = tools[i] || {};
    if (tool.type === "function" && tool.function) {
      var fn = tool.function;
      result.push({ type: "function", name: fn.name, description: fn.description, parameters: fn.parameters, strict: fn.strict });
    }
  }
  return result;
}

function onRequest(req) {
  if (!isChatCompletions(req.url) || req.method !== "POST") return;
  var chat;
  try { chat = JSON.parse(req.body); } catch (error) {
    return { status: 400, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ error: { message: "chat/completions body must be valid JSON", type: "invalid_request_error" } }) };
  }

  var input = [];
  var instructions = [];
  var messages = Array.isArray(chat.messages) ? chat.messages : [];
  for (var i = 0; i < messages.length; i++) {
    var message = messages[i] || {};
    var role = message.role || "user";
    if (role === "system" || role === "developer") {
      var instruction = textFromContent(message.content);
      if (instruction) instructions.push(instruction);
    } else if (role === "tool") {
      input.push({ type: "function_call_output", call_id: message.tool_call_id, output: textFromContent(message.content) });
    } else {
      var content = responseContent(message.content);
      if (content.length) input.push({ role: role, content: content });
      var calls = Array.isArray(message.tool_calls) ? message.tool_calls : [];
      for (var j = 0; j < calls.length; j++) {
        var call = calls[j] || {};
        var fn = call.function || {};
        if (call.id && fn.name) input.push({ type: "function_call", call_id: call.id, name: fn.name, arguments: fn.arguments || "{}" });
      }
    }
  }

  var responses = { model: chat.model, input: input, stream: chat.stream === true, temperature: chat.temperature, top_p: chat.top_p, max_output_tokens: chat.max_tokens, tools: responseTools(chat.tools), tool_choice: chat.tool_choice, parallel_tool_calls: chat.parallel_tool_calls, metadata: chat.metadata };
  if (instructions.length) responses.instructions = instructions.join("\n\n");
  req.streamResponse = chat.stream === true;
  req.url = req.url.replace("/v1/chat/completions", "/v1/responses");
  req.body = JSON.stringify(responses);
}

function onResponse(resp, req) {
  if (!isChatCompletions(req.originalPath) || resp.status < 200 || resp.status >= 300 || !resp.body) return;
  var response;
  try { response = JSON.parse(resp.body); } catch (error) { console.warn("Responses upstream returned non-JSON:", error.message); return; }
  var content = "", toolCalls = [], output = Array.isArray(response.output) ? response.output : [];
  for (var i = 0; i < output.length; i++) {
    var item = output[i] || {};
    if (item.type === "message") {
      var parts = Array.isArray(item.content) ? item.content : [];
      for (var j = 0; j < parts.length; j++) {
        if (parts[j].type === "output_text") content += parts[j].text || "";
        if (parts[j].type === "refusal") content += parts[j].refusal || "";
      }
    } else if (item.type === "function_call") toolCalls.push({ id: item.call_id, type: "function", function: { name: item.name, arguments: item.arguments || "{}" } });
  }
  var message = { role: "assistant", content: content || null };
  if (toolCalls.length) message.tool_calls = toolCalls;
  var usage = response.usage ? { prompt_tokens: response.usage.input_tokens, completion_tokens: response.usage.output_tokens, total_tokens: response.usage.total_tokens } : undefined;
  resp.body = JSON.stringify({ id: response.id, object: "chat.completion", created: Math.floor(Date.now() / 1000), model: response.model, choices: [{ index: 0, message: message, finish_reason: toolCalls.length ? "tool_calls" : (response.status === "completed" ? "stop" : null) }], usage: usage });
  resp.headers["Content-Type"] = "application/json";
}

function chatChunk(state, delta, finishReason, usage) {
  var result = { id: state.id || "chatcmpl-relay", object: "chat.completion.chunk", created: Math.floor(Date.now() / 1000), model: state.model, choices: [{ index: 0, delta: delta, finish_reason: finishReason || null }] };
  if (usage) result.usage = usage;
  return { data: JSON.stringify(result) };
}

function onResponseStart(resp, req) {
  if (!isChatCompletions(req.originalPath)) throw new Error("compatibility script only supports chat/completions");
  resp.headers["Content-Type"] = "text/event-stream";
  delete resp.headers["Content-Length"];
  return { roleSent: false, toolCalls: {}, nextToolIndex: 0, sawToolCall: false, id: null, model: null };
}

function onResponseEvent(event, state, req) {
  var value;
  try { value = JSON.parse(event.data); } catch (error) { return null; }
  var response = value.response || value;
  if (response.id) state.id = response.id;
  if (response.model) state.model = response.model;
  var out = [];
  function sendRole() { if (!state.roleSent) { out.push(chatChunk(state, { role: "assistant" })); state.roleSent = true; } }
  if (event.event === "response.output_text.delta") { sendRole(); out.push(chatChunk(state, { content: value.delta || "" })); }
  else if (event.event === "response.output_item.added" && value.item && value.item.type === "function_call") {
    sendRole(); var item = value.item, index = state.nextToolIndex++; state.toolCalls[item.id] = index; state.sawToolCall = true;
    out.push(chatChunk(state, { tool_calls: [{ index: index, id: item.call_id || item.id, type: "function", function: { name: item.name, arguments: item.arguments || "" } }] }));
  } else if (event.event === "response.function_call_arguments.delta") {
    sendRole(); var callIndex = state.toolCalls[value.item_id]; if (callIndex === undefined) callIndex = 0;
    out.push(chatChunk(state, { tool_calls: [{ index: callIndex, function: { arguments: value.delta || "" } }] }));
  } else if (event.event === "response.completed") {
    sendRole(); var usage = response.usage ? { prompt_tokens: response.usage.input_tokens, completion_tokens: response.usage.output_tokens, total_tokens: response.usage.total_tokens } : undefined;
    out.push(chatChunk(state, {}, state.sawToolCall ? "tool_calls" : "stop", usage));
  } else if (event.event === "error" || event.event === "response.failed") out.push({ event: "error", data: JSON.stringify({ error: value.error || value }) });
  return out;
}

function onResponseEnd(state, req) { return { data: "[DONE]" }; }
