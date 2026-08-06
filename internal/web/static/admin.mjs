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
    name.textContent = item.namespace || '(default)';
    name.href = item.namespace ? `/namespace/${encodeURIComponent(item.namespace)}/` : '/';
    const nameCell = document.createElement('td');
    nameCell.appendChild(name);
    row.appendChild(nameCell);
    row.appendChild(cell(`${item.protected ? 'protected' : 'public'} · ${item.policy}`));
    row.appendChild(cell(String(item.records || 0)));
    row.appendChild(cell(String(item.subscribers || 0)));
    row.appendChild(cell(item.lastAt ? new Date(item.lastAt).toLocaleString() : '-'));
    fragment.appendChild(row);
  }
  table.replaceChildren(fragment);
}

function applyState(state) {
  renderNamespaces(state.namespaces);
  permanentOption.hidden = !state.allowPermanentTokens;
  permanent.disabled = !state.allowPermanentTokens;
  if (state.defaultTokenTTL && !permanent.checked) ttl.value = state.defaultTokenTTL;
  if (state.maxTokenTTL) ttl.title = `最大有效期：${state.maxTokenTTL}`;
}

function cell(text) {
  const element = document.createElement('td');
  element.textContent = text;
  return element;
}

const events = new EventSource('events');
events.onopen = () => {
  status.textContent = 'live';
  status.className = 'conn conn-on';
};
events.onerror = () => {
  status.textContent = 'offline';
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
