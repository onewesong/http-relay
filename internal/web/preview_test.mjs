import test from 'node:test';
import assert from 'node:assert/strict';
import {
  HTML_PREVIEW_CSP,
  buildHTMLSrcdoc,
  makePreviewContext,
  mergeOpenAIEvents,
  parseResponseHead,
  parseSSE,
} from './static/preview/core.mjs';
import { selectPreviewPlugin } from './static/preview/viewer.mjs';
import { buildConversations, extractOpenAIExchange } from './static/conversation.mjs';

test('parses response headers case-insensitively', () => {
  const parsed = parseResponseHead('HTTP/1.1 200 OK\r\nContent-Type: application/problem+json; charset=utf-8\r\nX-Test: one\r\n\ttwo\r\n');
  assert.equal(parsed.statusLine, 'HTTP/1.1 200 OK');
  assert.equal(parsed.contentType, 'application/problem+json');
  assert.equal(parsed.headers['x-test'], 'one two');
});

test('parses standard SSE fields and multiline data', () => {
  const events = parseSSE(': ping\r\nid: 7\r\nevent: update\r\nretry: 1500\r\ndata: first\r\ndata: second\r\n\r\n');
  assert.deepEqual(events, [{ event: 'update', id: '7', retry: 1500, data: 'first\nsecond' }]);
});

test('merges OpenAI choices, content and fragmented tools', () => {
  const events = parseSSE([
    'data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Hel","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\\"city\\":"}}]}}]}',
    '',
    'data: {"choices":[{"index":0,"delta":{"content":"lo","tool_calls":[{"index":0,"function":{"arguments":"\\"Shanghai\\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":12}}',
    '',
    'data: [DONE]',
    '',
  ].join('\n'));
  const merged = mergeOpenAIEvents(events);
  assert.equal(merged.recognized, true);
  assert.equal(merged.done, true);
  assert.equal(merged.choices[0].content, 'Hello');
  assert.equal(merged.choices[0].toolCalls[0].function.name, 'weather');
  assert.equal(merged.choices[0].toolCalls[0].function.arguments, '{"city":"Shanghai"}');
  assert.equal(merged.choices[0].finishReason, 'tool_calls');
  assert.deepEqual(merged.usage, { total_tokens: 12 });
});

test('keeps invalid SSE data without preventing valid OpenAI merge', () => {
  const events = parseSSE('data: not-json\n\ndata: {"choices":[{"index":0,"delta":{"content":"ok"}}]}\n\n');
  const merged = mergeOpenAIEvents(events);
  assert.equal(merged.recognized, true);
  assert.deepEqual(merged.invalid, [0]);
  assert.equal(merged.choices[0].content, 'ok');
});

test('selects plugins using content type and syntax fallback', () => {
  const context = (head, text) => makePreviewContext({ head, body: { text, size: text.length, truncated: false } });
  assert.equal(selectPreviewPlugin(context('HTTP/1.1 200 OK\nContent-Type: text/event-stream', 'data: {}\n\n')).id, 'sse');
  assert.equal(selectPreviewPlugin(context('HTTP/1.1 200 OK\nContent-Type: text/html', '<p>hello</p>')).id, 'html');
  assert.equal(selectPreviewPlugin(context('', '{"ok":true}')).id, 'json');
  assert.equal(selectPreviewPlugin(context('HTTP/1.1 200 OK\nContent-Type: text/plain', 'plain')), null);
});

test('builds a sandbox document with CSP and a safe base URL', () => {
  const result = buildHTMLSrcdoc('<html><head><title>x</title></head><body>x</body></html>', 'https://example.test/a/');
  assert.match(result, /Content-Security-Policy/);
  assert.match(result, /<base href="https:\/\/example\.test\/a\/" target="_blank">/);
  assert.ok(result.indexOf('Content-Security-Policy') < result.indexOf('<title>'));
  assert.match(HTML_PREVIEW_CSP, /script-src 'none'/);
  assert.doesNotMatch(buildHTMLSrcdoc('<p>x</p>', 'javascript:alert(1)'), /<base /);
});

test('builds one conversation from successive Chat Completions histories', () => {
  const first = transaction(1,
    { model: 'gpt-test', messages: [{ role: 'user', content: 'Hello' }] },
    'data: {"id":"resp_1","model":"gpt-test","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"}}]}\n\ndata: [DONE]\n\n',
    'text/event-stream');
  const second = transaction(2,
    { model: 'gpt-test', messages: [
      { role: 'user', content: 'Hello' },
      { role: 'assistant', content: 'Hi' },
      { role: 'user', content: 'What next?' },
    ] },
    JSON.stringify({ id: 'resp_2', model: 'gpt-test', choices: [{ message: { role: 'assistant', content: 'Continue.' } }] }),
    'application/json');

  const conversations = buildConversations([first, second]);
  assert.equal(conversations.length, 1);
  assert.deepEqual(conversations[0].transactionIds, [1, 2]);
  assert.equal(conversations[0].confidence, 'inferred');
  assert.deepEqual(conversations[0].items.map((item) => [item.type, item.content]), [
    ['user', 'Hello'], ['agent', 'Hi'], ['user', 'What next?'], ['agent', 'Continue.'],
  ]);
});

test('links Responses API calls with previous_response_id', () => {
  const first = transaction(3,
    { model: 'gpt-test', input: 'First' },
    JSON.stringify({ id: 'response_a', model: 'gpt-test', output: [{ type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'One' }] }] }),
    'application/json', '/v1/responses');
  const second = transaction(4,
    { model: 'gpt-test', previous_response_id: 'response_a', input: 'Second' },
    JSON.stringify({ id: 'response_b', model: 'gpt-test', output: [{ type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'Two' }] }] }),
    'application/json', '/v1/responses');
  const conversations = buildConversations([first, second]);
  assert.equal(conversations.length, 1);
  assert.equal(conversations[0].confidence, 'exact');
  assert.deepEqual(conversations[0].items.map((item) => item.content), ['First', 'One', 'Second', 'Two']);
});

test('extracts tool calls and tool results as semantic events', () => {
  const exchange = extractOpenAIExchange(transaction(5, {
    model: 'gpt-test',
    messages: [
      { role: 'assistant', content: '', tool_calls: [{ id: 'call_1', type: 'function', function: { name: 'weather', arguments: '{"city":"Shanghai"}' } }] },
      { role: 'tool', name: 'weather', tool_call_id: 'call_1', content: '{"temperature":30}' },
    ],
  }, JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'It is 30°C.' } }] }), 'application/json'));
  assert.deepEqual(exchange.requestEntries.flatMap((entry) => entry.items).map((item) => item.type), ['tool_call', 'tool_result']);
  assert.equal(exchange.requestEntries[0].items[0].name, 'weather');
  assert.equal(exchange.requestEntries[1].items[0].toolCallId, 'call_1');
});

test('does not merge ambiguous parallel conversations', () => {
  const request = { model: 'gpt-test', messages: [{ role: 'user', content: 'same prompt' }] };
  const one = transaction(6, request, JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'branch one' } }] }), 'application/json');
  const two = transaction(7, request, JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'branch two' } }] }), 'application/json');
  assert.equal(buildConversations([one, two]).length, 2);
});

test('extracts Responses API SSE events and JSON errors', () => {
  const stream = [
    'event: response.created',
    'data: {"type":"response.created","response":{"id":"response_stream","model":"gpt-test"}}',
    '',
    'event: response.output_text.delta',
    'data: {"type":"response.output_text.delta","delta":"streamed text"}',
    '',
  ].join('\n');
  const streamed = extractOpenAIExchange(transaction(8, { model: 'gpt-test', input: 'stream' }, stream, 'text/event-stream', '/v1/responses'));
  assert.equal(streamed.responseId, 'response_stream');
  assert.equal(streamed.responseEntries[0].items[0].content, 'streamed text');

  const failed = extractOpenAIExchange(transaction(9, { model: 'gpt-test', messages: [{ role: 'user', content: 'fail' }] },
    JSON.stringify({ error: { type: 'rate_limit_error', message: 'Too many requests' } }), 'application/json'));
  assert.equal(failed.responseEntries[0].items[0].type, 'error');
  assert.equal(failed.responseEntries[0].items[0].content, 'Too many requests');
});

function transaction(seq, request, responseText, responseType, path = '/v1/chat/completions') {
  return {
    seq,
    at: `2026-07-31T12:00:0${seq % 10}Z`,
    target: `https://api.example.test${path}`,
    reqHead: `POST ${path} HTTP/1.1\r\nContent-Type: application/json\r\n`,
    reqBody: { text: JSON.stringify(request), size: JSON.stringify(request).length, truncated: false },
    respHead: `HTTP/1.1 200 OK\r\nContent-Type: ${responseType}\r\n`,
    respBody: { text: responseText, size: responseText.length, truncated: false },
  };
}
