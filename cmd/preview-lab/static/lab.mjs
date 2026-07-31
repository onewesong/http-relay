import { createBodyViewer } from '/preview/viewer.mjs';

const elements = Object.fromEntries(['fixture', 'reset', 'description', 'editor', 'target', 'status-line', 'headers', 'encoding', 'truncated', 'body', 'preview']
  .map((id) => [id, document.getElementById(id)]));
let fixtures = [];
let active = null;
let viewMode = 'preview';
let timer = 0;

fixtures = await fetch('/fixtures.json').then((response) => {
  if (!response.ok) throw new Error(`fixtures: HTTP ${response.status}`);
  return response.json();
});
fixtures.forEach((fixture, index) => {
  const option = document.createElement('option');
  option.value = String(index);
  option.textContent = fixture.name;
  elements.fixture.appendChild(option);
});

elements.fixture.onchange = () => loadFixture(Number(elements.fixture.value));
elements.reset.onclick = () => loadFixture(Number(elements.fixture.value));
elements.editor.onsubmit = (event) => event.preventDefault();
elements.editor.oninput = scheduleRender;
elements.editor.onchange = scheduleRender;
loadFixture(0);

function loadFixture(index) {
  active = structuredClone(fixtures[index]);
  elements.description.textContent = active.description;
  elements.target.value = active.target.replaceAll('{{origin}}', window.location.origin);
  elements['status-line'].value = active.statusLine;
  elements.headers.value = active.headers;
  elements.encoding.value = active.encoding;
  elements.truncated.checked = active.truncated;
  elements.body.value = active.body;
  render();
}

function scheduleRender() {
  clearTimeout(timer);
  timer = setTimeout(render, 150);
}

function render() {
  const encoding = elements.encoding.value;
  const value = elements.body.value;
  const size = encoding === 'base64' ? decodedSize(value) : new TextEncoder().encode(value).length;
  const body = { size, truncated: elements.truncated.checked };
  if (encoding === 'base64') body.base64 = value;
  else body.text = value;
  const head = [elements['status-line'].value, elements.headers.value].filter(Boolean).join('\r\n');
  const viewer = createBodyViewer({ target: elements.target.value, head, body }, {
    mode: viewMode,
    onModeChange: (mode) => { viewMode = mode; },
  });
  elements.preview.replaceChildren(viewer);
}

function decodedSize(value) {
  try {
    return atob(value.replace(/\s/g, '')).length;
  } catch {
    return new TextEncoder().encode(value).length;
  }
}
