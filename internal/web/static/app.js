'use strict';

import { tryParseJSON } from './preview/core.mjs';
import { createBodyViewer, createCopyButton, renderJSON } from './preview/viewer.mjs';
import { buildConversations } from './conversation.mjs';

const txns = new Map();   // seq -> transaction
const order = [];         // seqs sorted newest-first
let selected = null;
let selectedConversation = null;
let conversations = [];
let trafficView = 'requests';
let responseViewMode = loadResponseViewMode();
let clearing = false;

const listEl = document.getElementById('list');
const detailEl = document.getElementById('detail');
const metaEl = document.getElementById('meta');
const countEl = document.getElementById('count');
const statusEl = document.getElementById('status');
const logoutEl = document.getElementById('logout');
const clearEl = document.getElementById('clear');
const viewSwitchEl = document.getElementById('view-switch');

viewSwitchEl.querySelectorAll('button').forEach((button) => {
  button.onclick = () => setTrafficView(button.dataset.view);
});
clearEl.onclick = requestClear;

// ---- SSE ----
function connect() {
  const es = new EventSource('events');
  es.onopen = () => setConn(true);
  es.onerror = () => {
    setConn(false);
    // A rejected SSE request (such as an expired session) permanently closes
    // EventSource; send the user back through the login flow in that case.
    if (es.readyState === EventSource.CLOSED) {
      const next = window.location.pathname + window.location.search + window.location.hash;
      window.location.assign('/login?next=' + encodeURIComponent(next));
    }
  };
  es.onmessage = (e) => {
    let msg;
    try { msg = JSON.parse(e.data); } catch { return; }
    if (msg.type === 'meta') return applyMeta(msg.meta);
    if (msg.type === 'txn') return applyTxn(msg.txn);
    if (msg.type === 'clear') return clearTraffic();
  };
}

function setConn(on) {
  statusEl.textContent = on ? 'live' : 'offline';
  statusEl.className = 'conn ' + (on ? 'conn-on' : 'conn-off');
}

function applyMeta(meta) {
  if (!meta) return;
  const bits = [meta.namespace ? 'namespace=' + meta.namespace : 'namespace=(default)', meta.addr, meta.mode, 'proxy=' + meta.proxy, 'timeout=' + meta.timeout];
  metaEl.textContent = bits.filter(Boolean).join('  ·  ');
  if (meta.version) document.title = `http-relay ${meta.version}`;
  logoutEl.hidden = !meta.authEnabled;
}

function applyTxn(t) {
  const isNew = !txns.has(t.seq);
  txns.set(t.seq, t);
  if (isNew) {
    order.push(t.seq);
    order.sort(compareTransactionRecency);
  }
  if (selected === null && isNew) selected = t.seq;
  // Conversation recognition is chronological even though both visible lists
  // are newest-first. This preserves message/timeline ordering within a chat.
  conversations = buildConversations([...order].reverse().map((seq) => txns.get(seq)));
  conversations.sort((a, b) => timestamp(b.updatedAt) - timestamp(a.updatedAt));
  if (!conversations.some((conversation) => conversation.id === selectedConversation)) {
    selectedConversation = conversations[0]?.id || null;
  }

  const oldHeight = listEl.scrollHeight;
  const atTop = listEl.scrollTop < 24;
  renderList();
  if (isNew) {
    if (atTop) listEl.scrollTop = 0;
    else listEl.scrollTop += listEl.scrollHeight - oldHeight;
  }

  if (trafficView === 'requests') {
    if (t.seq === selected) renderDetail(t);
  } else {
    renderSelectedConversation();
  }

  updateCount();
}

function compareTransactionRecency(aSeq, bSeq) {
  const timeDifference = timestamp(txns.get(bSeq)?.at) - timestamp(txns.get(aSeq)?.at);
  if (timeDifference) return timeDifference;
  return Number(bSeq) - Number(aSeq);
}

function timestamp(value) {
  const parsed = Date.parse(value || '');
  return Number.isNaN(parsed) ? 0 : parsed;
}

// ---- list ----
function renderList() {
  if (trafficView === 'conversations') return renderConversationList();
  const frag = document.createDocumentFragment();
  for (const seq of order) {
    frag.appendChild(rowFor(txns.get(seq)));
  }
  listEl.replaceChildren(frag);
}

function rowFor(t) {
  const row = document.createElement('div');
  row.className = 'row' + (t.seq === selected ? ' sel' : '');
  row.onclick = () => select(t.seq);

  const seq = t.seq >= (2 ** 32) ? '-' : '#' + t.seq;
  row.appendChild(cell('seq', seq));
  row.appendChild(cell('method m-' + methodClass(t.method), t.method || '-'));
  row.appendChild(cell('status ' + statusClass(t), t.done ? String(t.status) : '···'));
  row.appendChild(cell('dur', t.done ? formatDuration(t.durationMs) : ''));
  row.appendChild(cell('time', formatClock(t.at)));

  const target = document.createElement('span');
  target.className = 'target';
  if (t.rewriteProfile) {
    const profile = document.createElement('span');
    profile.className = 'profile-badge';
    profile.textContent = '@' + t.rewriteProfile;
    target.appendChild(profile);
  }
  target.appendChild(document.createTextNode(t.target || firstLine(t.reqHead) || ''));
  if (t.err) {
    const err = document.createElement('span');
    err.className = 'err';
    err.textContent = '  ✗ ' + t.err;
    target.appendChild(err);
  }
  row.appendChild(target);
  return row;
}

function cell(cls, text) {
  const s = document.createElement('span');
  s.className = cls;
  s.textContent = text;
  return s;
}

function select(seq) {
  selected = seq;
  renderList();
  renderDetail(txns.get(seq));
}

function setTrafficView(view) {
  if (view !== 'requests' && view !== 'conversations') return;
  trafficView = view;
  viewSwitchEl.querySelectorAll('button').forEach((button) => {
    const active = button.dataset.view === view;
    button.classList.toggle('active', active);
    button.setAttribute('aria-pressed', String(active));
  });
  listEl.setAttribute('aria-label', view);
  renderList();
  if (view === 'requests') renderDetail(txns.get(selected));
  else renderSelectedConversation();
}

async function requestClear() {
  if (clearing || order.length === 0) return;
  clearing = true;
  clearEl.title = '';
  updateCount();
  try {
    const response = await fetch('api/transactions', { method: 'DELETE' });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    clearTraffic();
  } catch (error) {
    console.error('Failed to clear transactions', error);
    clearEl.title = `Clear failed: ${error.message || error}`;
  } finally {
    clearing = false;
    updateCount();
  }
}

function clearTraffic() {
  txns.clear();
  order.length = 0;
  selected = null;
  selectedConversation = null;
  conversations = [];
  renderList();
  const empty = document.createElement('div');
  empty.className = 'empty';
  empty.textContent = trafficView === 'requests' ? 'Select a request to inspect it.' : 'No conversation selected.';
  detailEl.replaceChildren(empty);
  updateCount();
}

function updateCount() {
  countEl.textContent = `${order.length} reqs${conversations.length ? ` · ${conversations.length} chats` : ''}`;
  clearEl.disabled = clearing || order.length === 0;
}

// ---- conversation projection ----
function renderConversationList() {
  const frag = document.createDocumentFragment();
  if (!conversations.length) {
    const empty = document.createElement('div');
    empty.className = 'empty conversation-empty';
    empty.textContent = 'No OpenAI conversations recognized yet.';
    frag.appendChild(empty);
  }
  for (const conversation of conversations) {
    const row = document.createElement('div');
    row.className = 'conversation-row' + (conversation.id === selectedConversation ? ' sel' : '');
    row.onclick = () => selectConversation(conversation.id);
    const title = document.createElement('div');
    title.className = 'conversation-title';
    title.textContent = conversation.title || conversation.id;
    const meta = document.createElement('div');
    meta.className = 'conversation-row-meta';
    meta.textContent = [conversation.model, conversation.endpoint, `${conversation.transactionIds.length} reqs`, formatClock(conversation.updatedAt)].filter(Boolean).join(' · ');
    row.append(title, meta);
    frag.appendChild(row);
  }
  listEl.replaceChildren(frag);
}

function selectConversation(id) {
  selectedConversation = id;
  renderConversationList();
  renderSelectedConversation();
}

function renderSelectedConversation() {
  const conversation = conversations.find((candidate) => candidate.id === selectedConversation);
  if (!conversation) {
    const empty = document.createElement('div');
    empty.className = 'empty';
    empty.textContent = 'No conversation selected.';
    detailEl.replaceChildren(empty);
    return;
  }
  detailEl.replaceChildren(conversationView(conversation));
}

function conversationView(conversation) {
  const root = document.createElement('div');
  root.className = 'conversation-view';
  const header = document.createElement('header');
  header.className = 'conversation-header';
  const title = document.createElement('h2');
  title.textContent = conversation.title || conversation.id;
  const meta = document.createElement('div');
  meta.className = 'conversation-meta';
  meta.textContent = [conversation.model, conversation.endpoint, conversation.externalId ? `id=${conversation.externalId}` : '', `${conversation.transactionIds.length} requests`, confidenceLabel(conversation.confidence)].filter(Boolean).join(' · ');
  header.append(title, meta);
  root.appendChild(header);

  const strip = document.createElement('div');
  strip.className = 'conversation-strip';
  strip.title = 'Conversation event distribution';
  for (const item of conversation.items) {
    const segment = document.createElement('span');
    segment.className = `strip-${item.type}`;
    strip.appendChild(segment);
  }
  root.appendChild(strip);

  if (conversation.truncated) {
    const warning = document.createElement('div');
    warning.className = 'preview-warning';
    warning.textContent = 'One or more captured bodies are truncated; this conversation may be incomplete.';
    root.appendChild(warning);
  }

  const timeline = document.createElement('div');
  timeline.className = 'conversation-timeline';
  conversation.items.forEach((item, index) => timeline.appendChild(conversationItem(item, index)));
  root.appendChild(timeline);

  if (conversation.usage) {
    const usage = document.createElement('details');
    usage.className = 'conversation-usage';
    const summary = document.createElement('summary');
    summary.textContent = 'Latest usage';
    usage.append(summary, renderJSON(conversation.usage));
    root.appendChild(usage);
  }
  return root;
}

function conversationItem(item, index) {
  const details = document.createElement('details');
  details.className = `conversation-event event-${item.type}`;
  details.open = item.type === 'user' || item.type === 'agent' || item.type === 'instruction' || item.content.length < 800;
  const summary = document.createElement('summary');
  const label = document.createElement('span');
  label.className = 'event-label';
  label.textContent = eventLabel(item);
  const preview = document.createElement('span');
  preview.className = 'event-preview';
  preview.textContent = collapsedPreview(item);
  const position = document.createElement('span');
  position.className = 'event-position';
  position.textContent = `#${index + 1}  ${formatClock(item.at)}`;
  summary.append(label, preview, position);
  details.appendChild(summary);

  const body = document.createElement('div');
  body.className = 'event-body';
  const parsed = (item.type === 'tool_call' || item.type === 'tool_result') ? tryParseJSON(item.content) : undefined;
  if (parsed !== undefined) body.appendChild(renderJSON(parsed));
  else {
    const content = document.createElement('pre');
    content.textContent = item.content || '(empty)';
    body.appendChild(content);
  }
  const source = document.createElement('button');
  source.type = 'button';
  source.className = 'source-request';
  source.textContent = `View request #${item.transactionId}`;
  source.onclick = () => {
    selected = item.transactionId;
    setTrafficView('requests');
  };
  body.appendChild(source);
  details.appendChild(body);
  return details;
}

function eventLabel(item) {
  if (item.type === 'agent') return 'Agent';
  if (item.type === 'user') return 'User';
  if (item.type === 'instruction') return item.role === 'developer' ? 'Developer' : 'System';
  if (item.type === 'tool_call') return item.name || 'tool_call';
  if (item.type === 'tool_result') return item.name || 'tool_result';
  if (item.type === 'error') return item.name || 'Error';
  return item.type;
}

function collapsedPreview(item) {
  const text = (item.type === 'tool_call' && item.name ? item.name + ' ' : '') + (item.content || '');
  const singleLine = text.replace(/\s+/g, ' ').trim();
  return singleLine.length > 120 ? singleLine.slice(0, 117) + '…' : singleLine;
}

function confidenceLabel(confidence) {
  if (confidence === 'exact') return 'exact linkage';
  if (confidence === 'inferred') return 'history matched';
  return 'single request';
}

// ---- detail ----
function renderDetail(t) {
  if (!t) return;
  const frag = document.createDocumentFragment();
  frag.appendChild(routeContext(t));
  frag.appendChild(section('request', '▶', t.reqHead, t.reqBody, t, false));
  frag.appendChild(section('response', '◀', t.respHead, t.respBody, t, true));
  if (!t.reqHead && !t.respHead && !t.reqBody && !t.respBody) {
    const e = document.createElement('div');
    e.className = 'empty';
    e.textContent = '(no captured body — request still in flight)';
    frag.appendChild(e);
  }
  detailEl.replaceChildren(frag);
}

function routeContext(t) {
  const context = document.createElement('div');
  context.className = 'route-context';

  const namespace = document.createElement('span');
  namespace.textContent = 'namespace=' + (t.namespace || '(default)');
  context.appendChild(namespace);

  if (t.rewriteProfile) {
    const profile = document.createElement('span');
    profile.className = 'profile-badge';
    profile.textContent = '@' + t.rewriteProfile;
    context.appendChild(profile);
  }
  return context;
}

function section(label, arrow, head, body, transaction, isResponse) {
  const block = document.createElement('div');
  block.className = 'block';
  if (!head && !body) { block.hidden = true; return block; }

  const h = document.createElement('h3');
  h.innerHTML = `<span class="arrow">${arrow}</span> ${label}`;
  block.appendChild(h);

  if (head) block.appendChild(renderHead(head));
  if (body) block.appendChild(isResponse ? renderResponseBody(transaction, head, body) : renderRequestBody(body));
  return block;
}

function renderHead(head) {
  const pre = document.createElement('div');
  pre.className = 'hdr';
  const lines = head.replace(/\r/g, '').split('\n').filter((l) => l !== '');
  lines.forEach((line, i) => {
    const div = document.createElement('div');
    const idx = line.indexOf(':');
    if (i === 0 || idx <= 0 || /\s/.test(line.slice(0, idx))) {
      div.className = i === 0 ? 'headline' : '';
      div.textContent = line;
    } else {
      const k = document.createElement('span');
      k.className = 'k';
      k.textContent = line.slice(0, idx);
      div.appendChild(k);
      div.appendChild(document.createTextNode(line.slice(idx)));
    }
    pre.appendChild(div);
  });
  return pre;
}

function renderRequestBody(body) {
  const wrap = document.createElement('div');
  const meta = document.createElement('div');
  meta.className = 'bodymeta';
  meta.textContent = `body=${formatBytes(body.size)}${body.truncated ? ' (truncated)' : ''}`;
  const toolbar = document.createElement('div');
  toolbar.className = 'viewer-toolbar';
  toolbar.append(meta, document.createElement('span'));
  toolbar.lastChild.className = 'viewer-spacer';
  toolbar.appendChild(createCopyButton(body.base64 !== undefined ? body.base64 || '' : body.text || ''));
  wrap.appendChild(toolbar);

  if (body.base64 !== undefined) {
    const note = document.createElement('pre');
    note.textContent = `binary body — ${body.size} bytes (not shown)`;
    wrap.appendChild(note);
    return wrap;
  }

  const text = body.text || '';
  const parsed = tryParseJSON(text);
  if (parsed !== undefined) {
    wrap.appendChild(renderJSON(parsed));
  } else {
    const pre = document.createElement('pre');
    pre.textContent = text;
    wrap.appendChild(pre);
  }
  return wrap;
}

function renderResponseBody(transaction, head, body) {
  return createBodyViewer({ transaction, target: transaction.target, head, body }, {
    mode: responseViewMode,
    onModeChange: (mode) => {
      responseViewMode = mode;
      try { sessionStorage.setItem('http-relay.response-view-mode', mode); } catch { /* storage may be disabled */ }
    },
  });
}

function loadResponseViewMode() {
  try { return sessionStorage.getItem('http-relay.response-view-mode') === 'raw' ? 'raw' : 'preview'; }
  catch { return 'preview'; }
}

// ---- helpers ----
function methodClass(m) {
  switch ((m || '').toUpperCase()) {
    case 'GET': case 'POST': case 'PUT': case 'PATCH': case 'DELETE': return (m || '').toUpperCase();
    default: return 'other';
  }
}

function statusClass(t) {
  if (!t.done) return 's-pending';
  const c = t.status;
  if (c >= 500) return 's-5';
  if (c >= 400) return 's-4';
  if (c >= 300) return 's-3';
  if (c >= 200) return 's-2';
  return 's-pending';
}

function formatDuration(ms) {
  if (ms >= 1000) return (ms / 1000).toFixed(2) + 's';
  return ms + 'ms';
}

function formatBytes(n) {
  if (n < 1024) return n + 'B';
  const units = ['K', 'M', 'G', 'T'];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return v.toFixed(1) + units[i] + 'B';
}

function firstLine(s) {
  if (!s) return '';
  const i = s.indexOf('\n');
  return (i >= 0 ? s.slice(0, i) : s).replace(/\r$/, '');
}

function formatClock(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

connect();
