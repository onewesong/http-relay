export function parseResponseHead(head = '') {
  const lines = String(head).replace(/\r/g, '').split('\n');
  const statusLine = lines.shift() || '';
  const headers = Object.create(null);
  let current = '';
  for (const line of lines) {
    if (!line) continue;
    if (/^[ \t]/.test(line) && current) {
      headers[current] += ' ' + line.trim();
      continue;
    }
    const colon = line.indexOf(':');
    if (colon <= 0) continue;
    current = line.slice(0, colon).trim().toLowerCase();
    const value = line.slice(colon + 1).trim();
    headers[current] = headers[current] ? `${headers[current]}, ${value}` : value;
  }
  const contentType = (headers['content-type'] || '').split(';', 1)[0].trim().toLowerCase();
  return { statusLine, headers, contentType };
}

export function makePreviewContext({ transaction = null, target = '', head = '', body = null } = {}) {
  const parsed = parseResponseHead(head);
  return {
    transaction,
    target: target || transaction?.target || '',
    head,
    statusLine: parsed.statusLine,
    headers: parsed.headers,
    contentType: parsed.contentType,
    body,
    text: body?.text ?? '',
    base64: body?.base64,
    truncated: Boolean(body?.truncated),
  };
}

export function tryParseJSON(text) {
  const value = String(text ?? '').trim();
  if (!value || (value[0] !== '{' && value[0] !== '[')) return undefined;
  try { return JSON.parse(value); } catch { return undefined; }
}

export function parseSSE(text) {
  const lines = String(text ?? '').replace(/\r\n?/g, '\n').split('\n');
  const events = [];
  let frame = newFrame();

  const dispatch = () => {
    if (!frame.touched) return;
    events.push({
      event: frame.event || 'message',
      id: frame.id,
      retry: frame.retry,
      data: frame.data.join('\n'),
    });
    frame = newFrame();
  };

  for (const line of lines) {
    if (line === '') { dispatch(); continue; }
    if (line.startsWith(':')) continue;
    const colon = line.indexOf(':');
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? '' : line.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    switch (field) {
      case 'data': frame.data.push(value); frame.touched = true; break;
      case 'event': frame.event = value; frame.touched = true; break;
      case 'id':
        if (!value.includes('\0')) { frame.id = value; frame.touched = true; }
        break;
      case 'retry':
        if (/^\d+$/.test(value)) { frame.retry = Number(value); frame.touched = true; }
        break;
    }
  }
  dispatch();
  return events;
}

function newFrame() {
  return { event: '', id: '', retry: null, data: [], touched: false };
}

export function mergeOpenAIEvents(events) {
  const choices = new Map();
  let usage = null;
  let recognized = false;
  let done = false;
  let responseId = '';
  let model = '';
  const invalid = [];

  for (let eventIndex = 0; eventIndex < events.length; eventIndex++) {
    const data = events[eventIndex].data.trim();
    if (data === '[DONE]') { done = true; continue; }
    if (!data) continue;
    let payload;
    try { payload = JSON.parse(data); } catch { invalid.push(eventIndex); continue; }
    responseId ||= typeof payload?.id === 'string' ? payload.id : '';
    model ||= typeof payload?.model === 'string' ? payload.model : '';
    if (payload?.usage) usage = payload.usage;
    if (!Array.isArray(payload?.choices)) continue;
    recognized = true;
    for (const incoming of payload.choices) {
      const index = Number.isInteger(incoming.index) ? incoming.index : 0;
      if (!choices.has(index)) choices.set(index, emptyChoice(index));
      const choice = choices.get(index);
      const delta = incoming.delta || {};
      if (typeof delta.role === 'string') choice.role = delta.role;
      appendContent(choice, delta.content);
      if (incoming.finish_reason != null) choice.finishReason = incoming.finish_reason;
      if (delta.function_call) appendFunction(choice.functionCall, delta.function_call);
      for (const part of delta.tool_calls || []) appendToolCall(choice, part);
    }
  }

  return {
    recognized,
    done,
    responseId,
    model,
    usage,
    invalid,
    choices: [...choices.values()].sort((a, b) => a.index - b.index),
  };
}

function emptyChoice(index) {
  return { index, role: '', content: '', finishReason: null, functionCall: { name: '', arguments: '' }, toolCalls: [] };
}

function appendContent(choice, content) {
  if (typeof content === 'string') { choice.content += content; return; }
  if (!Array.isArray(content)) return;
  for (const part of content) {
    if (typeof part === 'string') choice.content += part;
    else if (typeof part?.text === 'string') choice.content += part.text;
  }
}

function appendFunction(target, part) {
  if (typeof part.name === 'string') target.name += part.name;
  if (typeof part.arguments === 'string') target.arguments += part.arguments;
}

function appendToolCall(choice, part) {
  const index = Number.isInteger(part.index) ? part.index : choice.toolCalls.length;
  let target = choice.toolCalls.find((tool) => tool.index === index);
  if (!target) {
    target = { index, id: '', type: '', function: { name: '', arguments: '' } };
    choice.toolCalls.push(target);
    choice.toolCalls.sort((a, b) => a.index - b.index);
  }
  if (typeof part.id === 'string') target.id += part.id;
  if (typeof part.type === 'string') target.type = part.type;
  if (part.function) appendFunction(target.function, part.function);
}

export const HTML_PREVIEW_CSP = [
  "default-src 'none'",
  "script-src 'none'",
  "style-src 'unsafe-inline' http: https: data:",
  'img-src http: https: data:',
  'font-src http: https: data:',
  "connect-src 'none'",
  "frame-src 'none'",
  "object-src 'none'",
  "form-action 'none'",
  'base-uri http: https:',
].join('; ');

export function buildHTMLSrcdoc(html, target = '') {
  const base = safeBaseURL(target);
  const prefix = `<meta http-equiv="Content-Security-Policy" content="${escapeAttribute(HTML_PREVIEW_CSP)}">` +
    (base ? `<base href="${escapeAttribute(base)}" target="_blank">` : '');
  const source = String(html ?? '');
  if (/<head(?:\s[^>]*)?>/i.test(source)) {
    return source.replace(/<head(?:\s[^>]*)?>/i, (match) => match + prefix);
  }
  return `<!doctype html><html><head>${prefix}</head><body>${source}</body></html>`;
}

function safeBaseURL(target) {
  try {
    const value = new URL(target);
    return value.protocol === 'http:' || value.protocol === 'https:' ? value.href : '';
  } catch { return ''; }
}

function escapeAttribute(value) {
  return String(value).replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;');
}
