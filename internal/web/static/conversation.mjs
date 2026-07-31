import { mergeOpenAIEvents, parseResponseHead, parseSSE, tryParseJSON } from './preview/core.mjs';

const conversationAdapters = [];

export function registerConversationAdapter(adapter) {
  if (!adapter?.id || typeof adapter.match !== 'function' || typeof adapter.extract !== 'function') {
    throw new TypeError('invalid conversation adapter');
  }
  if (conversationAdapters.some((item) => item.id === adapter.id)) throw new Error(`duplicate conversation adapter: ${adapter.id}`);
  conversationAdapters.push(adapter);
}

export function selectConversationAdapter(transaction) {
  let selected = null;
  let score = 0;
  for (const adapter of conversationAdapters) {
    let candidate = 0;
    try { candidate = Number(adapter.match(transaction)) || 0; } catch { candidate = 0; }
    if (candidate > score) { selected = adapter; score = candidate; }
  }
  return selected;
}

export function extractOpenAIExchange(transaction) {
  const request = textJSON(transaction?.reqBody);
  if (!request || typeof request !== 'object') return null;

  const requestEntries = requestMessageEntries(request);
  const target = transaction.target || '';
  const looksOpenAI = requestEntries.length > 0 || /\/(?:chat\/completions|responses)(?:\?|$)/.test(target);
  if (!looksOpenAI) return null;

  const response = extractResponse(transaction?.respHead || '', transaction?.respBody);
  const requestHead = parseResponseHead(transaction?.reqHead || '');
  const explicitKey = firstString(
    request.conversation_id,
    request.thread_id,
    request.session_id,
    request.metadata?.conversation_id,
    request.metadata?.thread_id,
    requestHead.headers['x-conversation-id'],
    requestHead.headers['x-thread-id'],
    requestHead.headers['x-session-id'],
  );

  return {
    transactionId: transaction.seq,
    at: transaction.at || '',
    target,
    endpoint: endpointName(target),
    model: firstString(request.model, response.model),
    explicitKey,
    previousResponseId: firstString(request.previous_response_id),
    responseId: response.id,
    requestEntries,
    responseEntries: response.entries,
    usage: response.usage,
    truncated: Boolean(transaction.reqBody?.truncated || transaction.respBody?.truncated),
  };
}

export function buildConversations(transactions) {
  const conversations = [];
  const explicit = new Map();
  const responses = new Map();

  for (const transaction of transactions) {
    const adapter = selectConversationAdapter(transaction);
    let exchange = null;
    try { exchange = adapter?.extract(transaction) || null; } catch { exchange = null; }
    if (!exchange) continue;

    let conversation = exchange.explicitKey ? explicit.get(exchange.explicitKey) : null;
    let confidence = conversation ? 'exact' : '';
    if (!conversation && exchange.previousResponseId) {
      conversation = responses.get(exchange.previousResponseId) || null;
      if (conversation) confidence = 'exact';
    }
    if (!conversation) {
      conversation = continuationCandidate(conversations, exchange);
      if (conversation) confidence = 'inferred';
    }
    if (!conversation) {
      conversation = newConversation(conversations.length + 1, exchange);
      conversations.push(conversation);
      confidence = exchange.explicitKey ? 'exact' : 'isolated';
    }

    appendExchange(conversation, exchange, confidence);
    if (exchange.explicitKey) explicit.set(exchange.explicitKey, conversation);
    if (exchange.responseId) responses.set(exchange.responseId, conversation);
  }

  return conversations;
}

function continuationCandidate(conversations, exchange) {
  const requestKeys = exchange.requestEntries.map((entry) => entry.key);
  if (!requestKeys.length) return null;
  const candidates = conversations.filter((conversation) =>
    conversation.hasResponse &&
    conversation.endpoint === exchange.endpoint &&
    (!conversation.model || !exchange.model || conversation.model === exchange.model) &&
    requestKeys.length >= conversation.historyKeys.length &&
    startsWith(requestKeys, conversation.historyKeys));
  return candidates.length === 1 ? candidates[0] : null;
}

function newConversation(number, exchange) {
  return {
    id: `conversation-${number}`,
    externalId: exchange.explicitKey || '',
    confidence: 'isolated',
    endpoint: exchange.endpoint,
    model: exchange.model,
    startedAt: exchange.at,
    updatedAt: exchange.at,
    transactionIds: [],
    items: [],
    historyKeys: [],
    truncated: false,
    usage: null,
    title: '',
    hasResponse: false,
  };
}

function appendExchange(conversation, exchange, confidence) {
  const requestKeys = exchange.requestEntries.map((entry) => entry.key);
  let additionStart = 0;
  if (startsWith(requestKeys, conversation.historyKeys)) additionStart = conversation.historyKeys.length;

  for (const entry of exchange.requestEntries.slice(additionStart)) {
    for (const item of entry.items) conversation.items.push(withTransaction(item, exchange, 'request'));
  }
  for (const entry of exchange.responseEntries) {
    for (const item of entry.items) conversation.items.push(withTransaction(item, exchange, 'response'));
  }

  if (startsWith(requestKeys, conversation.historyKeys)) conversation.historyKeys = requestKeys.slice();
  else if (requestKeys.length) conversation.historyKeys.push(...requestKeys);
  conversation.historyKeys.push(...exchange.responseEntries.map((entry) => entry.key));
  if (!conversation.transactionIds.includes(exchange.transactionId)) conversation.transactionIds.push(exchange.transactionId);
  conversation.externalId ||= exchange.explicitKey;
  conversation.model ||= exchange.model;
  conversation.updatedAt = exchange.at || conversation.updatedAt;
  conversation.truncated ||= exchange.truncated;
  conversation.usage = exchange.usage || conversation.usage;
  conversation.hasResponse ||= exchange.responseEntries.length > 0;
  if (confidence === 'exact' || conversation.confidence === 'isolated') conversation.confidence = confidence;
  conversation.title ||= titleFor(conversation.items);
}

function withTransaction(item, exchange, source) {
  return { ...item, transactionId: exchange.transactionId, at: exchange.at, source };
}

function requestMessageEntries(request) {
  if (Array.isArray(request.messages)) return request.messages.map(messageEntry).filter(Boolean);
  if (typeof request.input === 'string') return [messageEntry({ role: 'user', content: request.input })];
  if (Array.isArray(request.input)) return request.input.map(messageEntry).filter(Boolean);
  return [];
}

function extractResponse(head, body) {
  const contentType = parseResponseHead(head).contentType;
  if (contentType === 'text/event-stream' && body?.text !== undefined) {
    return extractStreamResponse(body.text);
  }
  const payload = textJSON(body);
  if (!payload) return { id: '', model: '', entries: [], usage: null };
  return responseFromPayload(payload);
}

function extractStreamResponse(text) {
  const events = parseSSE(text);
  const merged = mergeOpenAIEvents(events);
  if (merged.recognized) {
    return {
      id: merged.responseId || '',
      model: merged.model || '',
      usage: merged.usage,
      entries: merged.choices.map((choice) => messageEntry({
        role: choice.role || 'assistant',
        content: choice.content,
        tool_calls: choice.toolCalls.map((tool) => ({ id: tool.id, type: tool.type, function: tool.function })),
        function_call: choice.functionCall.name || choice.functionCall.arguments ? choice.functionCall : undefined,
      })).filter(Boolean),
    };
  }
  return extractResponsesStream(events);
}

function extractResponsesStream(events) {
  let id = '', model = '', usage = null, content = '';
  const calls = new Map();
  let finalPayload = null;
  for (const event of events) {
    const payload = tryParseJSON(event.data);
    if (!payload || typeof payload !== 'object') continue;
    const response = payload.response;
    id ||= firstString(response?.id, payload.response_id);
    model ||= firstString(response?.model, payload.model);
    usage = response?.usage || payload.usage || usage;
    if (payload.type === 'response.completed' && response) finalPayload = response;
    if (payload.type === 'response.output_text.delta' && typeof payload.delta === 'string') content += payload.delta;
    if (payload.type === 'response.output_item.added' && payload.item?.type === 'function_call') {
      calls.set(payload.output_index ?? calls.size, {
        id: payload.item.call_id || payload.item.id || '',
        type: 'function',
        function: { name: payload.item.name || '', arguments: payload.item.arguments || '' },
      });
    }
    if (payload.type === 'response.function_call_arguments.delta') {
      const key = payload.output_index ?? 0;
      const call = calls.get(key) || { id: payload.item_id || '', type: 'function', function: { name: '', arguments: '' } };
      call.function.arguments += payload.delta || '';
      calls.set(key, call);
    }
  }
  if (finalPayload) return responseFromPayload(finalPayload);
  const entries = [];
  if (content || calls.size) entries.push(messageEntry({ role: 'assistant', content, tool_calls: [...calls.values()] }));
  return { id, model, usage, entries: entries.filter(Boolean) };
}

function responseFromPayload(payload) {
  if (payload.error) {
    const content = typeof payload.error === 'string' ? payload.error : payload.error.message || stableStringify(payload.error);
    return {
      id: firstString(payload.id), model: firstString(payload.model), usage: payload.usage || null,
      entries: [{ key: stableStringify({ error: payload.error }), items: [{ type: 'error', role: 'error', content, name: payload.error.type || '' }] }],
    };
  }
  const messages = [];
  if (Array.isArray(payload.choices)) {
    for (const choice of payload.choices) if (choice?.message) messages.push(choice.message);
  } else if (Array.isArray(payload.output)) {
    for (const output of payload.output) {
      if (output?.type === 'message') messages.push(output);
      else if (output?.type === 'function_call') {
        messages.push({ role: 'assistant', tool_calls: [{ id: output.call_id || output.id, type: 'function', function: { name: output.name, arguments: output.arguments } }] });
      }
    }
  }
  return {
    id: firstString(payload.id),
    model: firstString(payload.model),
    usage: payload.usage || null,
    entries: messages.map(messageEntry).filter(Boolean),
  };
}

function messageEntry(message) {
  if (!message || typeof message !== 'object') return null;
  const role = message.role || (message.type === 'message' ? 'assistant' : 'user');
  const content = contentText(message.content);
  const items = [];
  if (content) items.push({ type: itemType(role), role, content, name: message.name || '' });
  for (const tool of message.tool_calls || []) {
    items.push({
      type: 'tool_call', role: 'assistant', name: tool.function?.name || tool.name || '',
      content: tool.function?.arguments || tool.arguments || '', toolCallId: tool.id || tool.call_id || '',
    });
  }
  if (message.function_call) {
    items.push({ type: 'tool_call', role: 'assistant', name: message.function_call.name || '', content: message.function_call.arguments || '', toolCallId: '' });
  }
  if (role === 'tool' && !content) items.push({ type: 'tool_result', role, content: '', toolCallId: message.tool_call_id || '' });
  if (role === 'tool') for (const item of items) item.toolCallId = message.tool_call_id || item.toolCallId || '';
  if (!items.length) return null;
  return { key: messageKey(message, role, content), items };
}

function messageKey(message, role, content) {
  return stableStringify({
    role,
    content,
    tool_call_id: message.tool_call_id || '',
    tool_calls: (message.tool_calls || []).map((tool) => ({
      id: tool.id || tool.call_id || '',
      name: tool.function?.name || tool.name || '',
      arguments: tool.function?.arguments || tool.arguments || '',
    })),
    function_call: message.function_call ? {
      name: message.function_call.name || '',
      arguments: message.function_call.arguments || '',
    } : null,
  });
}

function itemType(role) {
  if (role === 'assistant') return 'agent';
  if (role === 'tool') return 'tool_result';
  if (role === 'system' || role === 'developer') return 'instruction';
  return 'user';
}

function contentText(content) {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return '';
  return content.map((part) => {
    if (typeof part === 'string') return part;
    if (typeof part?.text === 'string') return part.text;
    if (typeof part?.input_text === 'string') return part.input_text;
    if (typeof part?.output_text === 'string') return part.output_text;
    if (part?.type === 'image_url' || part?.type === 'input_image') return '[image]';
    return '';
  }).filter(Boolean).join('\n');
}

function textJSON(body) {
  if (!body || body.base64 !== undefined) return undefined;
  return tryParseJSON(body.text || '');
}

function endpointName(target) {
  try { return new URL(target).pathname; } catch { return target.split('?', 1)[0]; }
}

function titleFor(items) {
  const item = items.find((candidate) => candidate.type === 'user') || items[0];
  const text = (item?.content || item?.name || 'OpenAI conversation').replace(/\s+/g, ' ').trim();
  return text.length > 80 ? text.slice(0, 77) + '…' : text;
}

function firstString(...values) {
  return values.find((value) => typeof value === 'string' && value) || '';
}

function startsWith(values, prefix) {
  return prefix.length <= values.length && prefix.every((value, index) => values[index] === value);
}

function stableStringify(value) {
  if (Array.isArray(value)) return '[' + value.map(stableStringify).join(',') + ']';
  if (value && typeof value === 'object') {
    return '{' + Object.keys(value).sort().map((key) => JSON.stringify(key) + ':' + stableStringify(value[key])).join(',') + '}';
  }
  return JSON.stringify(value);
}

registerConversationAdapter({
  id: 'openai',
  label: 'OpenAI',
  match(transaction) {
    const request = textJSON(transaction?.reqBody);
    if (!request || typeof request !== 'object') return 0;
    const target = transaction.target || '';
    if (/\/(?:chat\/completions|responses)(?:\?|$)/.test(target)) return 100;
    if (Array.isArray(request.messages) || request.input !== undefined) return 60;
    return 0;
  },
  extract: extractOpenAIExchange,
});
