/**
 * codegrapher.dev — repo viewer
 * ES module, no build step. Dispatched from index.html on DOMContentLoaded.
 *
 * Routes:
 *   /                          → landing (index.html, no JS needed here)
 *   /github.com/{org}/{repo}   → viewer: tree mode
 *   /github.com/{org}/{repo}/{path…}  → viewer: file mode
 *   ?q=pattern                 → search filter
 */

import { parseIngr } from './vendor/ingr-codec.js';

// ─── Security ───────────────────────────────────────────────────────────────

const ALLOWED_FORGES = ['github.com'];
const SAFE_PATH_RE = /^[A-Za-z0-9._\-/]+$/;

function sanitizePath(raw) {
  if (raw.includes('..') || raw.includes('//')) return null;
  if (!SAFE_PATH_RE.test(raw)) return null;
  return raw;
}

// ─── Router ─────────────────────────────────────────────────────────────────

function parseRoute() {
  // Strip leading slash, split on '/'
  const parts = location.pathname.replace(/^\//, '').split('/').filter(Boolean);
  if (parts.length === 0) return { mode: 'landing' };

  const forge = parts[0];
  // First segment: if it contains a dot → forge route
  if (!forge.includes('.')) return { mode: 'landing' };

  if (!ALLOWED_FORGES.includes(forge)) {
    return { mode: 'unsupported', forge };
  }

  const org = parts[1];
  const repo = parts[2];
  if (!org || !repo) return { mode: 'landing' };

  const rawPath = sanitizePath(parts.slice(3).join('/'));
  // sanitizePath returns null on bad input; empty string is fine (tree root)
  if (rawPath === null && parts.length > 3) {
    return { mode: 'error', message: 'Invalid path.' };
  }

  const q = new URLSearchParams(location.search).get('q') || '';
  const filePath = parts.slice(3).join('/');

  return {
    mode: filePath ? 'file' : 'tree',
    forge, org, repo, filePath, q,
  };
}

// ─── Entry ───────────────────────────────────────────────────────────────────
// type="module" scripts are deferred — DOM is ready when this runs.

const route = parseRoute();

if (route.mode === 'landing') {
  // nothing — landing page is fully static HTML
} else if (route.mode === 'unsupported') {
  renderUnsupported(route.forge);
} else if (route.mode === 'error') {
  renderTopLevelError(route.message);
} else {
  mountViewer(route);
}

// ─── Viewer mount ────────────────────────────────────────────────────────────

function mountViewer(route) {
  // Replace the page body with the viewer shell
  document.title = `${route.org}/${route.repo} — CodeGrapher`;

  // Update header nav for viewer context
  const navLinks = document.querySelector('.nav-links');
  if (navLinks) {
    navLinks.innerHTML = `
      <a href="/${route.forge}/${route.org}/${route.repo}" aria-label="Back to tree">
        ${route.org}/<strong>${route.repo}</strong>
      </a>
      <a class="nav-cta" href="https://github.com/${route.org}/${route.repo}" target="_blank" rel="noopener">GitHub</a>
    `;
  }

  const main = document.getElementById('main');
  if (!main) return;

  // Clear landing content, render viewer shell
  main.innerHTML = `
    <div class="viewer-layout container" id="viewer-root">
      <div class="viewer-search-bar">
        <label for="q-input" class="sr-only">Search files</label>
        <input
          id="q-input"
          type="search"
          class="viewer-search"
          placeholder="Search files… (/ to focus, Esc to clear)"
          autocomplete="off"
          spellcheck="false"
          value="${escapeHtml(route.q)}"
        />
        <span class="viewer-search-hint" id="search-hint"></span>
      </div>
      <div class="viewer-body">
        <nav class="viewer-tree" id="viewer-tree" aria-label="File tree">
          <div class="viewer-loading">Loading snapshot…</div>
        </nav>
        <main class="viewer-content" id="viewer-content" aria-label="File content">
          ${route.mode === 'tree'
            ? '<div class="viewer-welcome"><p>Select a file to view its contents.</p></div>'
            : '<div class="viewer-loading">Loading file…</div>'
          }
        </main>
      </div>
    </div>
  `;

  // Keyboard shortcuts
  const qInput = document.getElementById('q-input');
  document.addEventListener('keydown', e => {
    if (e.key === '/' && document.activeElement !== qInput) {
      e.preventDefault();
      qInput.focus();
      qInput.select();
    } else if (e.key === 'Escape' && document.activeElement === qInput) {
      qInput.value = '';
      qInput.dispatchEvent(new Event('input'));
      qInput.blur();
    }
  });

  loadViewer(route, qInput);
}

// ─── Data loading ─────────────────────────────────────────────────────────────

async function loadViewer(route, qInput) {
  const { forge, org, repo, filePath, q } = route;
  const rawBase = `https://raw.githubusercontent.com/${org}/${repo}/HEAD`;
  const snapshotUrl = `${rawBase}/codegrapher/files.ingr`;

  let filesData;
  try {
    const res = await fetch(snapshotUrl);
    if (!res.ok) {
      if (res.status === 404) {
        renderNoSnapshot(org, repo);
      } else {
        renderError(`Failed to fetch snapshot (HTTP ${res.status}).`);
      }
      return;
    }
    const text = await res.text();
    filesData = parseIngr(text);
  } catch (err) {
    renderError(`Could not load snapshot: ${err.message}`);
    return;
  }

  // Build file list from records
  const files = filesData.records.map(r => String(r['ID'] ?? r['id'] ?? ''));

  // Render tree
  const treeEl = document.getElementById('viewer-tree');
  const tree = buildTree(files);

  function renderTreeWithFilter(pattern) {
    const filtered = pattern ? filterFiles(files, pattern) : files;
    const hint = document.getElementById('search-hint');

    if (pattern && filtered.length === 0) {
      treeEl.innerHTML = '<div class="viewer-empty">No files match.</div>';
      if (hint) hint.textContent = '';
      return;
    }

    if (pattern && filtered.length === 1) {
      if (hint) hint.textContent = 'Press Enter to open';
    } else {
      if (hint) hint.textContent = filtered.length < files.length ? `${filtered.length} of ${files.length}` : '';
    }

    const displayTree = pattern ? buildTree(filtered) : tree;
    treeEl.innerHTML = '';
    treeEl.appendChild(renderTree(displayTree, forge, org, repo, filePath));
  }

  // Initial render
  renderTreeWithFilter(q);

  // Search input
  qInput.addEventListener('input', () => {
    const val = qInput.value.trim();
    history.replaceState(null, '', location.pathname + (val ? `?q=${encodeURIComponent(val)}` : ''));
    renderTreeWithFilter(val);
  });

  // Enter on single match → open it
  qInput.addEventListener('keydown', e => {
    if (e.key !== 'Enter') return;
    const val = qInput.value.trim();
    if (!val) return;
    const filtered = filterFiles(files, val);
    if (filtered.length === 1) {
      location.href = `/${forge}/${org}/${repo}/${filtered[0]}`;
    }
  });

  // File view
  if (filePath) {
    loadFile(rawBase, filePath);
  }
}

// ─── File content ─────────────────────────────────────────────────────────────

async function loadFile(rawBase, filePath) {
  const contentEl = document.getElementById('viewer-content');
  if (!contentEl) return;

  contentEl.innerHTML = '<div class="viewer-loading">Loading…</div>';

  try {
    const res = await fetch(`${rawBase}/${filePath}`);
    if (!res.ok) {
      contentEl.innerHTML = `<div class="viewer-error">Could not load file (HTTP ${res.status}).</div>`;
      return;
    }
    const text = await res.text();
    renderFile(contentEl, filePath, text);
  } catch (err) {
    contentEl.innerHTML = `<div class="viewer-error">Network error: ${escapeHtml(err.message)}</div>`;
  }
}

function renderFile(el, filePath, text) {
  const lines = text.split('\n');
  // Remove trailing empty line artifact
  if (lines[lines.length - 1] === '') lines.pop();

  const rows = lines.map((line, i) => {
    const ln = i + 1;
    return `<tr id="L${ln}"><td class="ln" data-ln="${ln}"><a href="#L${ln}">${ln}</a></td><td class="lc">${escapeHtml(line)}</td></tr>`;
  }).join('');

  const ext = filePath.split('.').pop() || '';

  el.innerHTML = `
    <div class="file-header">
      <span class="file-path">${escapeHtml(filePath)}</span>
      <span class="file-meta">${lines.length} lines · .${escapeHtml(ext)}</span>
    </div>
    <div class="file-code">
      <table class="code-table" aria-label="File contents"><tbody>${rows}</tbody></table>
    </div>
  `;

  // Scroll to line anchor if present
  const hash = location.hash;
  if (hash && hash.startsWith('#L')) {
    const target = el.querySelector(hash);
    if (target) target.scrollIntoView({ block: 'center' });
  }
}

// ─── Tree building ────────────────────────────────────────────────────────────

/**
 * @param {string[]} paths
 * @returns {object} nested tree: { dirs: { name → subtree }, files: string[] }
 */
function buildTree(paths) {
  const root = { dirs: {}, files: [] };
  for (const p of paths) {
    const parts = p.split('/');
    let node = root;
    for (let i = 0; i < parts.length - 1; i++) {
      const dir = parts[i];
      if (!node.dirs[dir]) node.dirs[dir] = { dirs: {}, files: [] };
      node = node.dirs[dir];
    }
    node.files.push(parts[parts.length - 1]);
  }
  return root;
}

/**
 * Renders the tree as a <ul> DOM element.
 */
function renderTree(node, forge, org, repo, activeFilePath, prefix = '') {
  const ul = document.createElement('ul');
  ul.className = 'tree-list';

  // Directories first
  for (const [dir, subtree] of Object.entries(node.dirs).sort()) {
    const li = document.createElement('li');
    li.className = 'tree-dir';

    const fullDir = prefix ? `${prefix}/${dir}` : dir;
    const isOpen = activeFilePath.startsWith(fullDir + '/') || activeFilePath === fullDir;

    const toggle = document.createElement('button');
    toggle.className = 'tree-dir-btn';
    toggle.setAttribute('aria-expanded', String(isOpen));
    toggle.setAttribute('aria-label', `${isOpen ? 'Collapse' : 'Expand'} ${dir}`);
    toggle.innerHTML = `<span class="tree-icon" aria-hidden="true">${isOpen ? '▾' : '▸'}</span><span class="tree-name">${escapeHtml(dir)}</span>`;

    const sub = renderTree(subtree, forge, org, repo, activeFilePath, fullDir);
    sub.style.display = isOpen ? '' : 'none';

    toggle.addEventListener('click', () => {
      const open = toggle.getAttribute('aria-expanded') === 'true';
      toggle.setAttribute('aria-expanded', String(!open));
      toggle.querySelector('.tree-icon').textContent = !open ? '▾' : '▸';
      sub.style.display = !open ? '' : 'none';
    });

    li.appendChild(toggle);
    li.appendChild(sub);
    ul.appendChild(li);
  }

  // Files
  for (const file of [...node.files].sort()) {
    const li = document.createElement('li');
    li.className = 'tree-file';

    const fullPath = prefix ? `${prefix}/${file}` : file;
    const isActive = activeFilePath === fullPath;

    const a = document.createElement('a');
    a.href = `/${forge}/${org}/${repo}/${fullPath}`;
    a.className = 'tree-file-link' + (isActive ? ' tree-file-active' : '');
    a.textContent = file;
    if (isActive) a.setAttribute('aria-current', 'page');

    li.appendChild(a);
    ul.appendChild(li);
  }

  return ul;
}

// ─── Search / filter ──────────────────────────────────────────────────────────

/**
 * Case-insensitive substring + glob (`*` wildcard) filter over file paths.
 * @param {string[]} files
 * @param {string} pattern
 * @returns {string[]}
 */
function filterFiles(files, pattern) {
  if (!pattern) return files;
  const lp = pattern.toLowerCase();
  if (lp.includes('*')) {
    const re = new RegExp(lp.split('*').map(escapeRegExp).join('.*'));
    return files.filter(f => re.test(f.toLowerCase()));
  }
  return files.filter(f => f.toLowerCase().includes(lp));
}

function escapeRegExp(s) {
  return s.replace(/[.+?^${}()|[\]\\]/g, '\\$&');
}

// ─── Error / empty states ────────────────────────────────────────────────────

function renderNoSnapshot(org, repo) {
  const treeEl = document.getElementById('viewer-tree');
  if (treeEl) {
    treeEl.innerHTML = `
      <div class="viewer-no-snapshot">
        <p><strong>${escapeHtml(org)}/${escapeHtml(repo)}</strong> has no CodeGrapher snapshot yet.</p>
        <p>To add one, run:</p>
        <pre class="no-snapshot-cmd">codegrapher init &amp;&amp; codegrapher export\ngit add codegrapher/ &amp;&amp; git commit -m "chore: add codegrapher snapshot"</pre>
        <p class="viewer-hint">The snapshot is fetched from <code>codegrapher/files.ingr</code> in the repo.</p>
      </div>
    `;
  }
  const contentEl = document.getElementById('viewer-content');
  if (contentEl) contentEl.innerHTML = '';
}

function renderError(msg) {
  const treeEl = document.getElementById('viewer-tree');
  if (treeEl) treeEl.innerHTML = `<div class="viewer-error">${escapeHtml(msg)}</div>`;
}

function renderUnsupported(forge) {
  const main = document.getElementById('main');
  if (main) {
    main.innerHTML = `
      <div class="container" style="padding:80px 24px;text-align:center">
        <p class="viewer-error">Unsupported source host: <code>${escapeHtml(forge)}</code>.</p>
        <p>Only <code>github.com</code> is currently supported.</p>
        <a class="btn btn-ghost" href="/" style="margin-top:24px;display:inline-flex">← Back to home</a>
      </div>
    `;
  }
}

function renderTopLevelError(msg) {
  const main = document.getElementById('main');
  if (main) {
    main.innerHTML = `
      <div class="container" style="padding:80px 24px;text-align:center">
        <p class="viewer-error">${escapeHtml(msg)}</p>
        <a class="btn btn-ghost" href="/" style="margin-top:24px;display:inline-flex">← Back to home</a>
      </div>
    `;
  }
}

// ─── Utilities ────────────────────────────────────────────────────────────────

function escapeHtml(str) {
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
