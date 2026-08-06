'use strict';

const table = document.getElementById('namespaces');
const status = document.getElementById('status');
const form = document.getElementById('issue-form');
const message = document.getElementById('issue-message');
const tokenResult = document.getElementById('token-result');
const token = document.getElementById('token');
const permanent = document.getElementById('permanent');
const permanentOption = document.getElementById('permanent-option');
const ttl = document.getElementById('ttl');

function renderNamespaces(items) {
  const fragment = document.createDocumentFragment();
  for (const item of items || []) {
    const row = document.createElement('tr');
    const name = document.createElement('a');
    name.textContent = item.namespace || '(默认视图)';
    name.href = item.namespace ? `/namespace/${encodeURIComponent(item.namespace)}/` : '/';
    name.className = 'namespace-link';
    const nameCell = document.createElement('td');
    nameCell.appendChild(name);
    row.appendChild(nameCell);
    row.appendChild(policyCell(item));
    row.appendChild(cell(String(item.records || 0), 'number-cell'));
    row.appendChild(cell(String(item.subscribers || 0), 'number-cell'));
    row.appendChild(cell(formatLastAt(item.lastAt), 'last-at'));
    fragment.appendChild(row);
  }
  table.replaceChildren(fragment);
}

function applyState(state) {
  renderNamespaces(state.namespaces);
  permanentOption.hidden = !state.allowPermanentTokens;
  permanent.disabled = !state.allowPermanentTokens;
  if (state.defaultTokenTTL && !permanent.checked) ttl.value = formatDuration(state.defaultTokenTTL);
  if (state.maxTokenTTL) ttl.title = `最大有效期：${formatDuration(state.maxTokenTTL)}`;
}

function cell(text, className = '') {
  const element = document.createElement('td');
  element.textContent = text;
  if (className) element.className = className;
  return element;
}

function policyCell(item) {
  const element = document.createElement('td');
  element.className = 'policy-cell';
  const access = document.createElement('span');
  access.className = `badge ${item.protected ? 'badge-protected' : 'badge-public'}`;
  access.textContent = item.protected ? '受保护' : '公开';
  const source = document.createElement('span');
  source.className = 'badge badge-policy';
  source.textContent = ({ default: '默认策略', fallback: '回退策略', explicit: '单独配置' })[item.policy] || item.policy;
  element.append(access, source);
  return element;
}

function formatLastAt(value) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime()) || date.getFullYear() <= 1) return '—';
  return date.toLocaleString();
}

function formatDuration(value) {
  return String(value).replace(/0s$/, '').replace(/0m$/, '');
}

const events = new EventSource('events');
events.onopen = () => {
  status.textContent = '在线';
  status.className = 'conn conn-on';
};
events.onerror = () => {
  status.textContent = '离线';
  status.className = 'conn conn-off';
  if (events.readyState === EventSource.CLOSED) window.location.assign('/login');
};
events.onmessage = (event) => {
  let data;
  try { data = JSON.parse(event.data); } catch { return; }
  if (data.type === 'namespaces') applyState(data);
};

permanent.onchange = () => { ttl.disabled = permanent.checked; };

form.onsubmit = async (event) => {
  event.preventDefault();
  discardToken();
  message.textContent = '';
  message.className = '';
  const body = {
    namespace: form.elements.namespace.value,
    ttl: permanent.checked ? '' : ttl.value,
    permanent: permanent.checked,
  };
  try {
    const response = await fetch('api/tokens', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!response.ok) throw new Error(await response.text() || `HTTP ${response.status}`);
    const result = await response.json();
    token.value = result.token;
    tokenResult.hidden = false;
    message.textContent = result.protected ? 'Token 已创建。' : 'Token 已创建；注意：该 namespace 当前为公开状态。';
    message.className = result.protected ? '' : 'warning';
  } catch (error) {
    message.textContent = `创建失败：${error.message || error}`;
    message.className = 'error';
  }
};

document.getElementById('copy').onclick = async () => {
  if (!token.value) return;
  await navigator.clipboard.writeText(token.value);
  message.textContent = '已复制。';
};
document.getElementById('discard').onclick = discardToken;

function discardToken() {
  token.value = '';
  tokenResult.hidden = true;
}
