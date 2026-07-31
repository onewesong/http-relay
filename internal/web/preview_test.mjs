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
