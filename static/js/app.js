'use strict';

// ── State ─────────────────────────────────────────────────────────────────────
let pendingRoots = [];
let currentPath = '/';

// ── DOM refs ──────────────────────────────────────────────────────────────────
const rootList    = document.getElementById('rootList');
const applyBtn    = document.getElementById('applyBtn');
const applyStatus = document.getElementById('applyStatus');
const treeEl      = document.getElementById('tree');
const breadcrumb  = document.getElementById('breadcrumb');
const sseEndpoint = document.getElementById('sseEndpoint');

// ── Init ──────────────────────────────────────────────────────────────────────
sseEndpoint.textContent = window.location.origin + '/sse';
applyBtn.addEventListener('click', applyRoots);
loadRoots();
browse('/');

// ── Roots ─────────────────────────────────────────────────────────────────────
async function loadRoots() {
  try {
    const res = await fetch('/api/roots');
    pendingRoots = await res.json() || [];
  } catch (e) {
    pendingRoots = [];
  }
  renderRoots();
}

function renderRoots() {
  rootList.innerHTML = '';
  if (pendingRoots.length === 0) {
    const li = document.createElement('li');
    li.className = 'root-empty';
    li.textContent = 'No roots configured';
    rootList.appendChild(li);
    return;
  }
  pendingRoots.forEach((root, i) => {
    const li  = document.createElement('li');
    li.className = 'root-item';

    const span = document.createElement('span');
    span.textContent = root;
    span.title = root;

    const btn = document.createElement('button');
    btn.className = 'remove-btn';
    btn.textContent = '×';
    btn.title = 'Remove';
    btn.addEventListener('click', () => {
      pendingRoots.splice(i, 1);
      renderRoots();
    });

    li.appendChild(span);
    li.appendChild(btn);
    rootList.appendChild(li);
  });
}

function addRoot(path) {
  if (!pendingRoots.includes(path)) {
    pendingRoots.push(path);
    renderRoots();
    setStatus('+ ' + path + ' staged — click Apply to save', 'ok');
  } else {
    setStatus('Already in roots list', '');
  }
}

async function applyRoots() {
  applyBtn.disabled = true;
  setStatus('Applying…', '');
  try {
    const res = await fetch('/api/roots', {
      method:  'POST',
      headers: { 'Content-Type': 'application/json' },
      body:    JSON.stringify(pendingRoots),
    });
    const data = await res.json();
    if (res.ok) {
      setStatus('✓ Applied — ' + (data.roots || pendingRoots).length + ' root(s) active', 'ok');
    } else {
      setStatus('✗ ' + (data.error || res.statusText), 'err');
    }
  } catch (e) {
    setStatus('✗ ' + e.message, 'err');
  }
  applyBtn.disabled = false;
}

function setStatus(msg, cls) {
  applyStatus.textContent = msg;
  applyStatus.className   = 'status' + (cls ? ' ' + cls : '');
}

// ── Browser tree ──────────────────────────────────────────────────────────────
async function browse(path) {
  currentPath = path;
  renderBreadcrumb(path);
  treeEl.innerHTML = '<div class="tree-loading">Loading…</div>';

  let entries;
  try {
    const res = await fetch('/api/browse?path=' + encodeURIComponent(path));
    entries = await res.json();
  } catch (e) {
    treeEl.innerHTML = '<div class="tree-loading">Error: ' + e.message + '</div>';
    return;
  }

  treeEl.innerHTML = '';

  // Up row (not at root)
  if (path !== '/') {
    const up = document.createElement('div');
    up.className = 'tree-item';

    const icon = document.createElement('span');
    icon.className = 'icon';
    icon.textContent = '↑';

    const name = document.createElement('span');
    name.className = 'name';
    name.textContent = '..';

    const parentPath = path.replace(/\/[^\/]+$/, '') || '/';
    up.appendChild(icon);
    up.appendChild(name);
    up.addEventListener('click', () => browse(parentPath));
    treeEl.appendChild(up);
  }

  (entries || []).forEach(e => {
    const div = document.createElement('div');
    div.className = 'tree-item';

    const icon = document.createElement('span');
    icon.className = 'icon';
    icon.textContent = '📁';

    const name = document.createElement('span');
    name.className = 'name';
    name.textContent = e.name;
    name.title = e.path;

    const addBtn = document.createElement('button');
    addBtn.className = 'add-btn';
    addBtn.textContent = '+ Add';
    addBtn.title = 'Add as root';
    // Closure over e.path — no string interpolation in HTML
    addBtn.addEventListener('click', ev => {
      ev.stopPropagation();
      addRoot(e.path);
    });

    div.appendChild(icon);
    div.appendChild(name);
    div.appendChild(addBtn);
    // Click row → navigate into it
    div.addEventListener('click', () => browse(e.path));
    treeEl.appendChild(div);
  });

  if ((entries || []).length === 0 && path !== '/') {
    const empty = document.createElement('div');
    empty.className = 'tree-loading';
    empty.textContent = 'No subdirectories';
    treeEl.appendChild(empty);
  }
}

function renderBreadcrumb(path) {
  breadcrumb.innerHTML = '';

  const parts = path === '/' ? [''] : path.split('/');
  // parts[0] is always '' (leading slash)
  let cumulative = '';

  parts.forEach((part, i) => {
    if (i === 0) {
      const seg = document.createElement('span');
      seg.className = 'bc-seg';
      seg.textContent = '/';
      seg.addEventListener('click', () => browse('/'));
      breadcrumb.appendChild(seg);
    } else {
      cumulative += '/' + part;
      const sep = document.createElement('span');
      sep.className = 'bc-sep';
      sep.textContent = '/';
      breadcrumb.appendChild(sep);

      const seg = document.createElement('span');
      seg.className = 'bc-seg';
      seg.textContent = part;
      const dest = cumulative; // closure capture
      seg.addEventListener('click', () => browse(dest));
      breadcrumb.appendChild(seg);
    }
  });
}
