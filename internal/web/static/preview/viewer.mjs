import { buildHTMLSrcdoc, makePreviewContext, mergeOpenAIEvents, parseSSE, tryParseJSON } from './core.mjs';

const plugins = [];

export function registerPreviewPlugin(plugin) {
  if (!plugin?.id || typeof plugin.match !== 'function' || typeof plugin.render !== 'function') {
    throw new TypeError('invalid preview plugin');
  }
  if (plugins.some((item) => item.id === plugin.id)) throw new Error(`duplicate preview plugin: ${plugin.id}`);
  plugins.push(plugin);
}

export function selectPreviewPlugin(context) {
  let selected = null;
  let score = 0;
  for (const plugin of plugins) {
    let candidate = 0;
    try { candidate = Number(plugin.match(context)) || 0; } catch { candidate = 0; }
    if (candidate > score) { selected = plugin; score = candidate; }
  }
  return selected;
}

export function createBodyViewer(input, options = {}) {
  const context = input?.contentType !== undefined ? input : makePreviewContext(input);
  const root = document.createElement('div');
  root.className = 'body-viewer';
  let mode = options.mode === 'raw' ? 'raw' : 'preview';

  const draw = () => {
    root.replaceChildren();
    const plugin = selectPreviewPlugin(context);
    const toolbar = document.createElement('div');
    toolbar.className = 'viewer-toolbar';
    const info = document.createElement('span');
    info.className = 'bodymeta';
    const size = context.body?.size ?? byteLength(context.text);
    info.textContent = `body=${formatBytes(size)}${context.truncated ? ' (truncated)' : ''}`;
    toolbar.appendChild(info);
    if (plugin) toolbar.appendChild(badge(plugin.label));
    const spacer = document.createElement('span');
    spacer.className = 'viewer-spacer';
    toolbar.appendChild(spacer);
    if (typeof options.onViewConversation === 'function') {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'mode-button conversation-button';
      button.textContent = 'View conversation';
      button.title = 'Open the conversation containing this request';
      button.onclick = options.onViewConversation;
      toolbar.appendChild(button);
    }
    toolbar.appendChild(modeButton('Preview', 'preview', plugin));
    toolbar.appendChild(modeButton('Raw', 'raw', true));
    root.appendChild(toolbar);

    const content = document.createElement('div');
    content.className = 'viewer-content';

    if (context.truncated) {
      const warning = document.createElement('div');
      warning.className = 'preview-warning';
      warning.textContent = 'Captured body is truncated; this preview may be incomplete.';
      root.appendChild(warning);
    }

    if (mode === 'raw' || !plugin) {
      if (mode === 'preview' && !plugin) content.appendChild(note('No preview plugin matched. Showing raw response.'));
      content.appendChild(renderRaw(context));
      content.appendChild(createCopyButton(copyPayload(context)));
      root.appendChild(content);
      return;
    }
    try {
      content.appendChild(plugin.render(context));
      content.appendChild(createCopyButton(copyPayload(context)));
      root.appendChild(content);
    } catch (error) {
      root.querySelectorAll('.mode-button').forEach((button) => {
        const active = button.textContent === 'Raw';
        button.classList.toggle('active', active);
        button.setAttribute('aria-pressed', String(active));
      });
      content.appendChild(note(`Preview failed: ${error?.message || error}. Showing raw response.`, 'preview-error'));
      content.appendChild(renderRaw(context));
      content.appendChild(createCopyButton(copyPayload(context)));
      root.appendChild(content);
    }
  };

  function modeButton(label, value, enabled) {
    const button = document.createElement('button');
    button.type = 'button';
    const effectiveMode = selectPreviewPlugin(context) ? mode : 'raw';
    button.className = 'mode-button' + (effectiveMode === value ? ' active' : '');
    button.textContent = label;
    button.disabled = !enabled;
    button.setAttribute('aria-pressed', String(effectiveMode === value));
    button.onclick = () => {
      mode = value;
      options.onModeChange?.(mode);
      draw();
    };
    return button;
  }

  draw();
  return root;
}

function renderRaw(context) {
  const pre = document.createElement('pre');
  pre.className = 'raw-body';
  if (context.base64 !== undefined) {
    pre.textContent = `binary body (base64)\n${context.base64 || ''}`;
  } else {
    pre.textContent = context.text;
  }
  return pre;
}

function copyPayload(context) {
  if (context.base64 !== undefined) return context.base64 || '';
  return context.text || '';
}

export function createCopyButton(text) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'mode-button copy-button';
  button.textContent = 'Copy';
  button.title = 'Copy body';
  button.onclick = async () => {
    try {
      await copyText(text);
      button.textContent = 'Copied';
      setTimeout(() => { button.textContent = 'Copy'; }, 1200);
    } catch {
      button.textContent = 'Copy failed';
      setTimeout(() => { button.textContent = 'Copy'; }, 1600);
    }
  };
  return button;
}

async function copyText(text) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.position = 'fixed';
  area.style.opacity = '0';
  document.body.appendChild(area);
  area.select();
  const copied = document.execCommand('copy');
  area.remove();
  if (!copied) throw new Error('copy failed');
}

function badge(label) {
  const value = document.createElement('span');
  value.className = 'preview-badge';
  value.textContent = label;
  return value;
}

function note(text, className = '') {
  const value = document.createElement('div');
  value.className = `preview-note ${className}`.trim();
  value.textContent = text;
  return value;
}

function byteLength(text) {
  return new TextEncoder().encode(text || '').length;
}

function formatBytes(n) {
  if (n < 1024) return n + 'B';
  const units = ['K', 'M', 'G', 'T'];
  let value = n / 1024, index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index++; }
  return value.toFixed(1) + units[index] + 'B';
}

export function renderJSON(value) {
  const pre = document.createElement('pre');
  pre.className = 'json';
  pre.appendChild(buildMember([], value, false));
  return pre;
}

function buildMember(label, value, comma) {
  const isArray = Array.isArray(value);
  const isObject = value && typeof value === 'object';
  if (isArray || isObject) return buildContainer(label, value, isArray, comma);
  const line = document.createElement('div');
  label.forEach((node) => line.appendChild(node));
  line.appendChild(scalar(value));
  if (comma) line.appendChild(punct(','));
  return line;
}

function buildContainer(label, value, isArray, comma) {
  const open = isArray ? '[' : '{';
  const close = isArray ? ']' : '}';
  const keys = isArray ? null : Object.keys(value);
  const count = isArray ? value.length : keys.length;
  const tail = close + (comma ? ',' : '');
  if (!count) {
    const line = document.createElement('div');
    label.forEach((node) => line.appendChild(node));
    line.appendChild(punct(open + tail));
    return line;
  }
  const details = document.createElement('details');
  details.className = 'node';
  details.open = true;
  const summary = document.createElement('summary');
  summary.appendChild(twirl());
  label.forEach((node) => summary.appendChild(node));
  summary.appendChild(punct(open));
  const hint = document.createElement('span');
  hint.className = 'collapsed-hint';
  hint.textContent = ` … ${tail} ${count} ${isArray ? (count === 1 ? 'item' : 'items') : (count === 1 ? 'key' : 'keys')}`;
  summary.appendChild(hint);
  details.appendChild(summary);
  const indent = document.createElement('div');
  indent.className = 'indent';
  if (isArray) value.forEach((item, index) => indent.appendChild(buildMember([], item, index < count - 1)));
  else keys.forEach((key, index) => indent.appendChild(buildMember(keyLabel(key), value[key], index < count - 1)));
  details.appendChild(indent);
  details.appendChild(punct(tail));
  return details;
}

function keyLabel(key) {
  const label = document.createElement('span');
  label.className = 'jk';
  label.textContent = JSON.stringify(key);
  return [label, punct(': ')];
}

function scalar(value) {
  const node = document.createElement('span');
  if (value === null) { node.className = 'ju'; node.textContent = 'null'; }
  else if (typeof value === 'string') { node.className = 'js'; node.textContent = JSON.stringify(value); }
  else if (typeof value === 'number') { node.className = 'jn'; node.textContent = String(value); }
  else if (typeof value === 'boolean') { node.className = 'jb'; node.textContent = String(value); }
  else node.textContent = String(value);
  return node;
}

function punct(text) { const node = document.createElement('span'); node.className = 'jp'; node.textContent = text; return node; }
function twirl() { const node = document.createElement('span'); node.className = 'tw'; return node; }

registerPreviewPlugin({
  id: 'json', label: 'JSON',
  match: (context) => context.base64 === undefined &&
    (context.contentType === 'application/json' || context.contentType.endsWith('+json') ? 80 : tryParseJSON(context.text) !== undefined ? 50 : 0),
  render: (context) => renderJSON(JSON.parse(context.text)),
});

registerPreviewPlugin({
  id: 'html', label: 'HTML',
  match: (context) => context.base64 === undefined &&
    (context.contentType === 'text/html' || context.contentType === 'application/xhtml+xml' ? 90 : /^\s*(?:<!doctype\s+html|<html\b)/i.test(context.text) ? 60 : 0),
  render: (context) => {
    const iframe = document.createElement('iframe');
    iframe.className = 'html-preview';
    iframe.setAttribute('sandbox', '');
    iframe.referrerPolicy = 'no-referrer';
    iframe.title = 'Sandboxed HTML response preview';
    iframe.srcdoc = buildHTMLSrcdoc(context.text, context.target);
    return iframe;
  },
});

registerPreviewPlugin({
  id: 'sse', label: 'SSE',
  match: (context) => context.base64 === undefined &&
    (context.contentType === 'text/event-stream' ? 100 : /(?:^|\n)(?:data|event|id|retry):/.test(context.text) ? 70 : 0),
  render: renderSSE,
});

function renderSSE(context) {
  const events = parseSSE(context.text);
  const merged = mergeOpenAIEvents(events);
  const root = document.createElement('div');
  root.className = 'sse-preview';
  if (merged.recognized) {
    const heading = document.createElement('h4');
    heading.textContent = 'OpenAI fields';
    root.appendChild(heading);
    for (const choice of merged.choices) root.appendChild(renderChoice(choice));
    if (merged.usage) {
      const usage = document.createElement('details');
      usage.open = true;
      const summary = document.createElement('summary');
      summary.textContent = 'Usage';
      usage.append(summary, renderJSON(merged.usage));
      root.appendChild(usage);
    }
  }
  const details = document.createElement('details');
  details.className = 'sse-events';
  details.open = !merged.recognized;
  const summary = document.createElement('summary');
  summary.textContent = `Events (${events.length})${merged.done ? ' · DONE' : ''}`;
  details.appendChild(summary);
  events.forEach((event, index) => details.appendChild(renderEvent(event, index)));
  root.appendChild(details);
  return root;
}

function renderChoice(choice) {
  const article = document.createElement('article');
  article.className = 'merged-choice';
  const meta = document.createElement('div');
  meta.className = 'choice-meta';
  meta.textContent = `choice ${choice.index}${choice.role ? ` · ${choice.role}` : ''}${choice.finishReason ? ` · ${choice.finishReason}` : ''}`;
  article.appendChild(meta);
  if (choice.content) { const content = document.createElement('pre'); content.className = 'merged-content'; content.textContent = choice.content; article.appendChild(content); }
  if (choice.functionCall.name || choice.functionCall.arguments) article.appendChild(renderCall('function', choice.functionCall));
  for (const tool of choice.toolCalls) article.appendChild(renderCall(tool.id || `tool ${tool.index}`, tool.function));
  return article;
}

function renderCall(label, call) {
  const details = document.createElement('details');
  details.open = true;
  const summary = document.createElement('summary');
  summary.textContent = `${label}: ${call.name || '(unnamed)'}`;
  details.appendChild(summary);
  const parsed = tryParseJSON(call.arguments);
  details.appendChild(parsed === undefined ? Object.assign(document.createElement('pre'), { textContent: call.arguments }) : renderJSON(parsed));
  return details;
}

function renderEvent(event, index) {
  const row = document.createElement('details');
  row.className = 'sse-event';
  const summary = document.createElement('summary');
  summary.textContent = `#${index + 1} ${event.event}${event.id ? ` · id=${event.id}` : ''}`;
  row.appendChild(summary);
  const parsed = tryParseJSON(event.data);
  row.appendChild(parsed === undefined ? Object.assign(document.createElement('pre'), { textContent: event.data }) : renderJSON(parsed));
  return row;
}
