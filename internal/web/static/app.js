'use strict';

const txns = new Map();   // seq -> transaction
const order = [];         // seqs in arrival order
let selected = null;

const listEl = document.getElementById('list');
const detailEl = document.getElementById('detail');
const metaEl = document.getElementById('meta');
const countEl = document.getElementById('count');
const statusEl = document.getElementById('status');
const logoutEl = document.getElementById('logout');

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
  };
}

function setConn(on) {
  statusEl.textContent = on ? 'live' : 'offline';
  statusEl.className = 'conn ' + (on ? 'conn-on' : 'conn-off');
}

function applyMeta(meta) {
  if (!meta) return;
  const bits = [meta.addr, meta.mode, 'proxy=' + meta.proxy, 'timeout=' + meta.timeout];
  metaEl.textContent = bits.filter(Boolean).join('  ·  ');
  if (meta.version) document.title = `http-relay ${meta.version}`;
  logoutEl.hidden = !meta.authEnabled;
}

function applyTxn(t) {
  const isNew = !txns.has(t.seq);
  txns.set(t.seq, t);
  if (isNew) order.push(t.seq);

  const atBottom = listEl.scrollHeight - listEl.scrollTop - listEl.clientHeight < 24;
  renderList();
  if (isNew && atBottom) listEl.scrollTop = listEl.scrollHeight;

  if (selected === null && isNew) select(t.seq);
  else if (t.seq === selected) renderDetail(t);

  countEl.textContent = `${order.length} reqs`;
}

// ---- list ----
function renderList() {
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

  const target = document.createElement('span');
  target.className = 'target';
  target.textContent = t.target || firstLine(t.reqHead) || '';
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

// ---- detail ----
function renderDetail(t) {
  if (!t) return;
  const frag = document.createDocumentFragment();
  frag.appendChild(section('request', '▶', t.reqHead, t.reqBody));
  frag.appendChild(section('response', '◀', t.respHead, t.respBody));
  if (!t.reqHead && !t.respHead && !t.reqBody && !t.respBody) {
    const e = document.createElement('div');
    e.className = 'empty';
    e.textContent = '(no captured body — request still in flight)';
    frag.replaceChildren(e);
  }
  detailEl.replaceChildren(frag);
}

function section(label, arrow, head, body) {
  const block = document.createElement('div');
  block.className = 'block';
  if (!head && !body) { block.hidden = true; return block; }

  const h = document.createElement('h3');
  h.innerHTML = `<span class="arrow">${arrow}</span> ${label}`;
  block.appendChild(h);

  if (head) block.appendChild(renderHead(head));
  if (body) block.appendChild(renderBody(body));
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

function renderBody(body) {
  const wrap = document.createElement('div');
  const meta = document.createElement('div');
  meta.className = 'bodymeta';
  meta.textContent = `body=${formatBytes(body.size)}${body.truncated ? ' (truncated)' : ''}`;
  wrap.appendChild(meta);

  if (body.base64 !== undefined) {
    const note = document.createElement('pre');
    note.textContent = `binary body — ${body.size} bytes (not shown)`;
    wrap.appendChild(note);
    return wrap;
  }

  const text = body.text || '';
  const parsed = tryJSON(text);
  if (parsed !== undefined) {
    wrap.appendChild(jsonTree(parsed));
  } else {
    const pre = document.createElement('pre');
    pre.textContent = text;
    wrap.appendChild(pre);
  }
  return wrap;
}

function tryJSON(text) {
  const t = text.trim();
  if (!t || (t[0] !== '{' && t[0] !== '[')) return undefined;
  try { return JSON.parse(t); } catch { return undefined; }
}

// ---- collapsible, highlighted JSON tree ----
function jsonTree(value) {
  const pre = document.createElement('pre');
  pre.className = 'json';
  pre.appendChild(buildMember([], value, false));
  return pre;
}

// buildMember renders one value, optionally prefixed by label nodes (a key) and
// followed by a comma. Containers become collapsible <details>; scalars a line.
function buildMember(label, value, comma) {
  const isArr = Array.isArray(value);
  const isObj = value && typeof value === 'object';
  if (isArr || isObj) return buildContainer(label, value, isArr, comma);

  const line = document.createElement('div');
  label.forEach((n) => line.appendChild(n));
  line.appendChild(scalar(value));
  if (comma) line.appendChild(punct(','));
  return line;
}

function buildContainer(label, value, isArr, comma) {
  const open = isArr ? '[' : '{';
  const close = isArr ? ']' : '}';
  const keys = isArr ? null : Object.keys(value);
  const count = isArr ? value.length : keys.length;
  const tail = close + (comma ? ',' : '');

  if (count === 0) {
    const line = document.createElement('div');
    label.forEach((n) => line.appendChild(n));
    line.appendChild(punct(open + tail));
    return line;
  }

  const details = document.createElement('details');
  details.className = 'node';
  details.open = true;

  const summary = document.createElement('summary');
  summary.appendChild(twirl());
  label.forEach((n) => summary.appendChild(n));
  summary.appendChild(punct(open));
  const hint = document.createElement('span');
  hint.className = 'collapsed-hint';
  const noun = isArr ? (count === 1 ? 'item' : 'items') : (count === 1 ? 'key' : 'keys');
  hint.textContent = ` … ${tail} ${count} ${noun}`;
  summary.appendChild(hint);
  details.appendChild(summary);

  const indent = document.createElement('div');
  indent.className = 'indent';
  if (isArr) {
    value.forEach((item, i) => indent.appendChild(buildMember([], item, i < count - 1)));
  } else {
    keys.forEach((key, i) => indent.appendChild(buildMember(keyLabel(key), value[key], i < count - 1)));
  }
  details.appendChild(indent);
  details.appendChild(punct(tail));
  return details;
}

function keyLabel(key) {
  const k = document.createElement('span');
  k.className = 'jk';
  k.textContent = JSON.stringify(key);
  return [k, punct(': ')];
}

function scalar(value) {
  const s = document.createElement('span');
  if (value === null) { s.className = 'ju'; s.textContent = 'null'; }
  else if (typeof value === 'string') { s.className = 'js'; s.textContent = JSON.stringify(value); }
  else if (typeof value === 'number') { s.className = 'jn'; s.textContent = String(value); }
  else if (typeof value === 'boolean') { s.className = 'jb'; s.textContent = String(value); }
  else { s.textContent = String(value); }
  return s;
}

function punct(text) {
  const s = document.createElement('span');
  s.className = 'jp';
  s.textContent = text;
  return s;
}

function twirl() {
  const s = document.createElement('span');
  s.className = 'tw';
  return s;
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

connect();
