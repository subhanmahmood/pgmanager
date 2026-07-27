// pgmanager web UI.
//
// Served by `pgmanager serve` from ./web on the same origin as /api, so the
// bearer token never crosses an origin boundary. The server sends a strict CSP
// (script-src 'self'; style-src 'self'), which is why there is no inline
// script, no inline style and no inline event handler anywhere in this UI —
// everything is wired up through delegated listeners below.

const API = '/api';
const TOKEN_KEY = 'pgmanager_token';

const state = {
  token: localStorage.getItem(TOKEN_KEY) || '',
  whoami: null,
  projects: [],
  // project name -> array of databases, populated lazily on expand.
  databases: {},
  expanded: new Set(),
  tokens: [],
  devices: [],
};

/* ------------------------------------------------------------------ dom */

const $ = (id) => document.getElementById(id);

function el(tag, attrs = {}, children = []) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === null || v === undefined || v === false) continue;
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else node.setAttribute(k, v === true ? '' : String(v));
  }
  for (const child of [].concat(children)) {
    if (child) node.appendChild(typeof child === 'string' ? document.createTextNode(child) : child);
  }
  return node;
}

let toastTimer;
function toast(message, isError = false) {
  const t = $('toast');
  t.textContent = message;
  t.className = 'toast' + (isError ? ' error' : '');
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3500);
}

function showError(id, err) {
  const node = $(id);
  node.textContent = err.message || String(err);
  node.hidden = false;
}

function openModal(id) {
  $(id).hidden = false;
}

function closeModal(id) {
  $(id).hidden = true;
}

/* ------------------------------------------------------------------ api */

class ApiError extends Error {
  constructor(message, status) {
    super(message);
    this.status = status;
  }
}

async function api(method, path, body) {
  const headers = {};
  if (state.token) headers['Authorization'] = `Bearer ${state.token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res;
  try {
    res = await fetch(API + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError('cannot reach the pgmanager server', 0);
  }

  if (res.status === 204) return null;

  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { /* non-JSON body */ }
  }

  if (!res.ok) {
    const message = (data && data.error) || text || `request failed (${res.status})`;
    throw new ApiError(message, res.status);
  }
  return data;
}

// Any 401/403 mid-session means the token was revoked or expired underneath
// us; drop it and go back to the login gate rather than showing empty views.
function handle(err) {
  if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
    signOut('Session expired — sign in again.');
    return;
  }
  toast(err.message || String(err), true);
}

/* --------------------------------------------------------------- format */

const fmtDate = (s) => (s ? new Date(s).toLocaleString(undefined, {
  year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit',
}) : '—');

// The API's URL segment for a database: "dev" or "pr_42".
const envSegment = (db) => (db.pr_number ? `pr_${db.pr_number}` : db.env);

const isExpired = (s) => !!s && new Date(s).getTime() < Date.now();

function relativeExpiry(s) {
  if (!s) return 'never';
  const ms = new Date(s).getTime() - Date.now();
  if (ms <= 0) return 'expired';
  const days = Math.floor(ms / 86400000);
  if (days >= 1) return `in ${days}d`;
  const hours = Math.floor(ms / 3600000);
  return hours >= 1 ? `in ${hours}h` : 'in <1h';
}

function envBadge(db) {
  return el('span', { class: `badge badge-${db.env}`, text: envSegment(db) });
}

/* ------------------------------------------------------------ secret ui */

function secretRow(label, value) {
  const input = el('input', { type: 'text', value, readonly: true });
  const copy = el('button', { type: 'button', class: 'ghost small', text: 'Copy' });
  copy.addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast('Copied');
    } catch {
      input.select();
      toast('Press ⌘/Ctrl+C to copy', true);
    }
  });
  return el('div', {}, [el('label', { text: label }), el('div', { class: 'secret' }, [input, copy])]);
}

function showSecret(title, warning, rows) {
  $('secretTitle').textContent = title;
  $('secretWarning').textContent = warning;
  const body = $('secretBody');
  body.replaceChildren(...rows.map(([label, value]) => secretRow(label, value)));
  openModal('modal-secret');
}

function showDatabaseCredentials(db) {
  showSecret(
    `${db.database_name}`,
    'These credentials grant full access to the database. Treat them as secrets.',
    [
      ['Database', db.database_name],
      ['User', db.user_name],
      ['Password', db.password],
      ['Host', db.host],
      ['Port', String(db.port)],
      ['Connection string', db.connection_string],
    ],
  );
}

/* ----------------------------------------------------------- confirm ui */

let confirmHandler = null;

function confirmAction(title, message, onConfirm) {
  $('confirmTitle').textContent = title;
  $('confirmBody').textContent = message;
  confirmHandler = onConfirm;
  openModal('modal-confirm');
}

$('confirmOk').addEventListener('click', async () => {
  const fn = confirmHandler;
  confirmHandler = null;
  closeModal('modal-confirm');
  if (fn) {
    try { await fn(); } catch (err) { handle(err); }
  }
});

/* ------------------------------------------------------------- projects */

async function loadProjects() {
  state.projects = (await api('GET', '/projects')) || [];
  renderProjects();
}

function renderProjects() {
  const list = $('projectsList');

  if (state.projects.length === 0) {
    list.replaceChildren(el('div', {
      class: 'empty',
      text: 'No projects yet — create one to get started.',
    }));
    return;
  }

  list.replaceChildren(...state.projects.map(renderProjectCard));
}

function renderProjectCard(project) {
  const open = state.expanded.has(project.name);

  const head = el('div', { class: 'card-head', 'data-action': 'toggle-project', 'data-project': project.name }, [
    el('span', { class: 'caret', text: open ? '▾' : '▸' }),
    el('span', { class: 'name', text: project.name }),
    el('span', { class: 'muted spacer', text: `created ${fmtDate(project.created_at)}` }),
    el('button', {
      class: 'ghost small', type: 'button',
      'data-action': 'new-database', 'data-project': project.name, text: 'New database',
    }),
    el('button', {
      class: 'danger small', type: 'button',
      'data-action': 'delete-project', 'data-project': project.name, text: 'Delete',
    }),
  ]);

  const card = el('div', { class: 'card' }, [head]);
  if (open) card.appendChild(renderDatabases(project.name));
  return card;
}

function renderDatabases(projectName) {
  const databases = state.databases[projectName];
  const body = el('div', { class: 'card-body' });

  if (databases === undefined) {
    body.appendChild(el('p', { class: 'muted', text: 'Loading…' }));
    return body;
  }
  if (databases.length === 0) {
    body.appendChild(el('p', { class: 'muted', text: 'No databases in this project.' }));
    return body;
  }

  const rows = databases.map((db) => {
    const segment = envSegment(db);
    const expiry = el('span', {
      class: isExpired(db.expires_at) ? 'badge badge-expired' : '',
      text: relativeExpiry(db.expires_at),
    });
    return el('tr', {}, [
      el('td', { class: 'mono', text: db.database_name }),
      el('td', {}, [envBadge(db)]),
      el('td', { class: 'mono', text: db.user_name }),
      el('td', { class: 'muted', text: fmtDate(db.created_at) }),
      el('td', { class: 'muted' }, [expiry]),
      el('td', { class: 'actions' }, [
        el('button', {
          class: 'ghost small', type: 'button', 'data-action': 'db-credentials',
          'data-project': projectName, 'data-env': segment, text: 'Credentials',
        }),
        el('button', {
          class: 'danger small', type: 'button', 'data-action': 'delete-database',
          'data-project': projectName, 'data-env': segment, 'data-name': db.database_name, text: 'Delete',
        }),
      ]),
    ]);
  });

  const header = el('tr', {}, ['Database', 'Environment', 'User', 'Created', 'Expires', '']
    .map((h) => el('th', { text: h })));

  body.appendChild(el('table', {}, [
    el('thead', {}, [header]),
    el('tbody', {}, rows),
  ]));
  return body;
}

async function loadDatabases(projectName) {
  state.databases[projectName] = (await api('GET', `/projects/${encodeURIComponent(projectName)}/databases`)) || [];
  renderProjects();
}

async function toggleProject(name) {
  if (state.expanded.has(name)) {
    state.expanded.delete(name);
    renderProjects();
    return;
  }
  state.expanded.add(name);
  renderProjects();          // show the "Loading…" placeholder immediately
  await loadDatabases(name);
}

/* --------------------------------------------------------------- tokens */

async function loadTokens() {
  state.tokens = (await api('GET', '/auth/tokens')) || [];
  renderTokens();
}

function renderTokens() {
  const list = $('tokensList');

  if (state.tokens.length === 0) {
    list.replaceChildren(el('div', { class: 'empty', text: 'No tokens.' }));
    return;
  }

  const rows = state.tokens.map((t) => {
    const revoked = !!t.revoked_at;
    const expired = isExpired(t.expires_at);
    let status = 'active';
    if (revoked) status = 'revoked';
    else if (expired) status = 'expired';

    return el('tr', {}, [
      el('td', { text: t.name }),
      el('td', { class: 'mono', text: t.token_prefix }),
      el('td', { class: 'mono', text: (t.scopes || []).join(', ') }),
      el('td', {}, [el('span', {
        class: status === 'active' ? 'badge badge-dev' : 'badge badge-expired',
        text: status,
      })]),
      el('td', { class: 'muted', text: fmtDate(t.created_at) }),
      el('td', { class: 'muted', text: t.last_used_at ? fmtDate(t.last_used_at) : 'never' }),
      el('td', { class: 'muted', text: relativeExpiry(t.expires_at) }),
      el('td', { class: 'actions' }, [
        revoked ? null : el('button', {
          class: 'danger small', type: 'button',
          'data-action': 'revoke-token', 'data-prefix': t.token_prefix, 'data-name': t.name,
          text: 'Revoke',
        }),
      ]),
    ]);
  });

  const header = el('tr', {}, ['Name', 'Prefix', 'Scopes', 'Status', 'Created', 'Last used', 'Expires', '']
    .map((h) => el('th', { text: h })));

  list.replaceChildren(el('div', { class: 'panel' }, [
    el('table', {}, [el('thead', {}, [header]), el('tbody', {}, rows)]),
  ]));
}

/* -------------------------------------------------------------- devices */

async function loadDevices() {
  state.devices = (await api('GET', '/auth/devices')) || [];
  renderDevices();
}

function renderDevices() {
  const list = $('devicesList');

  if (state.devices.length === 0) {
    list.replaceChildren(el('div', { class: 'empty', text: 'No devices waiting for approval.' }));
    return;
  }

  const rows = state.devices.map((d) => el('tr', {}, [
    el('td', { class: 'mono', text: d.user_code }),
    el('td', { text: d.client_name || '—' }),
    el('td', { class: 'mono', text: d.client_ip || '—' }),
    el('td', { class: 'mono', text: (d.requested_scopes || []).join(', ') || '—' }),
    el('td', { class: 'muted', text: relativeExpiry(d.expires_at) }),
    el('td', { class: 'actions' }, [
      el('button', {
        class: 'small', type: 'button',
        'data-action': 'review-device', 'data-code': d.user_code, text: 'Review',
      }),
    ]),
  ]));

  const header = el('tr', {}, ['Code', 'Client', 'IP', 'Requested scopes', 'Expires', '']
    .map((h) => el('th', { text: h })));

  list.replaceChildren(el('div', { class: 'panel' }, [
    el('table', {}, [el('thead', {}, [header]), el('tbody', {}, rows)]),
  ]));
}

// Opens the approval dialog for one waiting device.
async function reviewDevice(userCode) {
  const d = await api('GET', `/auth/device/${encodeURIComponent(userCode)}`);

  $('deviceDetail').replaceChildren(
    el('dt', { text: 'Code' }), el('dd', { class: 'mono', text: d.user_code }),
    el('dt', { text: 'Client' }), el('dd', { text: d.client_name || 'unknown' }),
    el('dt', { text: 'IP' }), el('dd', { class: 'mono', text: d.client_ip || 'unknown' }),
    el('dt', { text: 'Requested' }), el('dd', { class: 'mono', text: (d.requested_scopes || []).join('\n') || 'nothing specific' }),
    el('dt', { text: 'Expires' }), el('dd', { text: relativeExpiry(d.expires_at) }),
  );

  const form = $('formDevice');
  form.reset();
  form.dataset.code = d.user_code;
  $('deviceTokenName').value = d.client_name || `device-${d.user_code}`;
  // Prefill with what was asked for so the common case is one click, but the
  // approver still has to look at it.
  $('deviceScopes').value = (d.requested_scopes || []).join('\n');
  $('deviceError').hidden = true;
  openModal('modal-device');
  $('deviceScopes').focus();
}

/* ------------------------------------------------------------ views/nav */

const VIEWS = ['projects', 'tokens', 'devices', 'maintenance'];

// Loaders are best-effort: a non-admin token may legitimately lack the
// `tokens` scope, in which case that view just reports the failure.
const LOADERS = {
  projects: loadProjects,
  tokens: loadTokens,
  devices: loadDevices,
  maintenance: renderWhoami,
};

async function showView(name) {
  if (!VIEWS.includes(name)) name = 'projects';
  for (const v of VIEWS) $(`view-${v}`).hidden = v !== name;
  for (const tab of document.querySelectorAll('.tab')) {
    tab.classList.toggle('active', tab.dataset.view === name);
  }
  if (location.hash.slice(1) !== name) location.hash = name;

  try {
    await LOADERS[name]();
  } catch (err) {
    handle(err);
  }
}

function renderWhoami() {
  const dl = $('whoamiDetail');
  const who = state.whoami || { token_prefix: '—', scopes: [] };
  dl.replaceChildren(
    el('dt', { text: 'Token' }), el('dd', { text: who.token_prefix }),
    el('dt', { text: 'Scopes' }), el('dd', { text: (who.scopes || []).join('\n') || 'none' }),
  );
}

/* ---------------------------------------------------------------- health */

async function checkHealth() {
  const dot = $('healthDot');
  const text = $('healthText');
  try {
    await api('GET', '/health');
    dot.className = 'dot ok';
    text.textContent = 'connected';
  } catch {
    dot.className = 'dot bad';
    text.textContent = 'unreachable';
  }
}

/* ------------------------------------------------------------ auth flow */

async function signIn(token) {
  state.token = token;
  // whoami is the cheapest authenticated endpoint, and every valid token can
  // call it regardless of scope — so it is the right probe for "is this good?"
  state.whoami = await api('GET', '/auth/whoami');
  localStorage.setItem(TOKEN_KEY, token);

  $('login').hidden = true;
  $('app').hidden = false;
  $('whoamiPrefix').textContent = state.whoami.token_prefix;

  checkHealth();
  await showView(initialView());

  // Arriving from `pgmanager login` (/device?code=WXYZ-2468): the operator
  // followed a link to approve a specific device, so go straight there.
  if (deviceCodeFromURL) {
    $('deviceCode').value = deviceCodeFromURL;
    const code = deviceCodeFromURL;
    deviceCodeFromURL = '';
    try {
      await reviewDevice(code);
    } catch (err) {
      showError('deviceLookupError', err);
    }
  }
}

// The device-approval deep link lives at a real path (/device) rather than a
// hash, because that is what is printable in a terminal. Everything else is
// hash-routed.
let deviceCodeFromURL = location.pathname.replace(/\/+$/, '') === '/device'
  ? (new URLSearchParams(location.search).get('code') || '').trim()
  : '';

function initialView() {
  if (deviceCodeFromURL || location.pathname.replace(/\/+$/, '') === '/device') return 'devices';
  return location.hash.slice(1) || 'projects';
}

function signOut(message) {
  state.token = '';
  state.whoami = null;
  state.projects = [];
  state.databases = {};
  state.expanded.clear();
  state.tokens = [];
  state.devices = [];
  localStorage.removeItem(TOKEN_KEY);

  $('app').hidden = true;
  $('login').hidden = false;
  $('loginToken').value = '';
  const err = $('loginError');
  if (message) {
    err.textContent = message;
    err.hidden = false;
  } else {
    err.hidden = true;
  }
}

$('loginForm').addEventListener('submit', async (e) => {
  e.preventDefault();
  const token = $('loginToken').value.trim();
  $('loginError').hidden = true;
  if (!token) {
    showError('loginError', new Error('A token is required.'));
    return;
  }
  const submit = $('loginSubmit');
  submit.disabled = true;
  try {
    await signIn(token);
  } catch (err) {
    state.token = '';
    showError('loginError', err.status === 401 || err.status === 403
      ? new Error('That token was rejected.')
      : err);
  } finally {
    submit.disabled = false;
  }
});

$('logout').addEventListener('click', () => signOut());

/* ----------------------------------------------------------------- forms */

$('formProject').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('projectError').hidden = true;
  const name = $('projectName').value.trim();
  try {
    await api('POST', '/projects', { name });
    closeModal('modal-project');
    toast(`Project ${name} created`);
    await loadProjects();
  } catch (err) {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('projectError', err);
  }
});

$('dbEnv').addEventListener('change', () => {
  $('prNumberGroup').hidden = $('dbEnv').value !== 'pr';
});

$('formDatabase').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('databaseError').hidden = true;

  const project = $('formDatabase').dataset.project;
  const env = $('dbEnv').value;
  const body = { env };

  if (env === 'pr') {
    const n = parseInt($('prNumber').value, 10);
    if (!n || n < 1) {
      showError('databaseError', new Error('A positive PR number is required.'));
      return;
    }
    body.pr_number = n;
  }

  const extensions = $('dbExtensions').value.split(',').map((s) => s.trim()).filter(Boolean);
  if (extensions.length) body.extensions = extensions;

  try {
    const db = await api('POST', `/projects/${encodeURIComponent(project)}/databases`, body);
    closeModal('modal-database');
    toast(`Created ${db.database_name}`);
    state.expanded.add(project);
    await loadDatabases(project);
    // The password is only ever returned on create; surface it right away.
    showDatabaseCredentials(db);
  } catch (err) {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('databaseError', err);
  }
});

$('formToken').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('tokenError').hidden = true;

  const name = $('tokenName').value.trim();
  const scopes = $('tokenScopes').value.split('\n').map((s) => s.trim()).filter(Boolean);
  const expires = $('tokenExpires').value.trim();

  if (scopes.length === 0) {
    showError('tokenError', new Error('At least one scope is required.'));
    return;
  }

  try {
    const created = await api('POST', '/auth/tokens', { name, scopes, expires });
    closeModal('modal-token');
    await loadTokens();
    showSecret(
      `Token ${created.token_prefix}`,
      'Copy this now — the plaintext token is never shown again.',
      [['Token', created.token]],
    );
  } catch (err) {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('tokenError', err);
  }
});

$('formDeviceLookup').addEventListener('submit', (e) => {
  e.preventDefault();
  $('deviceLookupError').hidden = true;
  const code = $('deviceCode').value.trim();
  if (!code) {
    showError('deviceLookupError', new Error('Enter the code shown by the CLI.'));
    return;
  }
  reviewDevice(code).catch((err) => {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('deviceLookupError', err);
  });
});

$('formDevice').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('deviceError').hidden = true;

  const code = $('formDevice').dataset.code;
  const name = $('deviceTokenName').value.trim();
  const scopes = $('deviceScopes').value.split('\n').map((s) => s.trim()).filter(Boolean);
  const expires = $('deviceExpires').value.trim();

  if (scopes.length === 0) {
    showError('deviceError', new Error('At least one scope is required.'));
    return;
  }

  try {
    await api('POST', `/auth/device/${encodeURIComponent(code)}/approve`, { name, scopes, expires });
    closeModal('modal-device');
    // The token goes straight to the waiting device — it is never shown here.
    toast(`Approved ${code}`);
    $('deviceCode').value = '';
    await loadDevices();
    await loadTokens();
  } catch (err) {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('deviceError', err);
  }
});

$('deviceDeny').addEventListener('click', async () => {
  const code = $('formDevice').dataset.code;
  try {
    await api('POST', `/auth/device/${encodeURIComponent(code)}/deny`);
    closeModal('modal-device');
    toast(`Denied ${code}`);
    $('deviceCode').value = '';
    await loadDevices();
  } catch (err) {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('deviceError', err);
  }
});

/* --------------------------------------------------------- click routing */

const ACTIONS = {
  'new-project': () => {
    $('formProject').reset();
    $('projectError').hidden = true;
    openModal('modal-project');
    $('projectName').focus();
  },

  'delete-project': (data) => confirmAction(
    `Delete project ${data.project}?`,
    'This drops every database and role in the project. It cannot be undone.',
    async () => {
      await api('DELETE', `/projects/${encodeURIComponent(data.project)}`);
      state.expanded.delete(data.project);
      delete state.databases[data.project];
      toast(`Project ${data.project} deleted`);
      await loadProjects();
    },
  ),

  'toggle-project': (data) => toggleProject(data.project).catch(handle),

  'new-database': (data) => {
    const form = $('formDatabase');
    form.reset();
    form.dataset.project = data.project;
    $('dbProjectName').textContent = data.project;
    $('prNumberGroup').hidden = true;
    $('databaseError').hidden = true;
    openModal('modal-database');
  },

  'db-credentials': async (data) => {
    const path = `/projects/${encodeURIComponent(data.project)}/databases/${encodeURIComponent(data.env)}/credentials`;
    showDatabaseCredentials(await api('GET', path));
  },

  'delete-database': (data) => confirmAction(
    `Delete ${data.name}?`,
    'The database and its role are dropped. This cannot be undone.',
    async () => {
      await api('DELETE', `/projects/${encodeURIComponent(data.project)}/databases/${encodeURIComponent(data.env)}`);
      toast(`${data.name} deleted`);
      await loadDatabases(data.project);
    },
  ),

  'new-token': () => {
    $('formToken').reset();
    $('tokenError').hidden = true;
    openModal('modal-token');
    $('tokenName').focus();
  },

  'revoke-token': (data) => confirmAction(
    `Revoke ${data.name}?`,
    `Any client using token ${data.prefix} stops working immediately.`,
    async () => {
      await api('DELETE', `/auth/tokens/${encodeURIComponent(data.prefix)}`);
      toast('Token revoked');
      await loadTokens();
    },
  ),

  'review-device': (data) => reviewDevice(data.code),

  'refresh-devices': () => loadDevices(),

  cleanup: () => {
    const olderThan = $('cleanupOlderThan').value.trim() || '7d';
    confirmAction(
      'Run cleanup?',
      `Every PR database older than ${olderThan} will be dropped.`,
      async () => {
        const res = await api('POST', '/cleanup', { older_than: olderThan });
        const result = $('cleanupResult');
        result.replaceChildren(el('p', {
          class: 'muted',
          text: res.count === 0 ? 'Nothing to clean up.' : `Deleted ${res.count}: ${res.deleted.join(', ')}`,
        }));
        toast(`Cleanup removed ${res.count} database(s)`);
        // Any expanded project's DB list may now be stale.
        state.databases = {};
        state.expanded.clear();
      },
    );
  },
};

document.addEventListener('click', (e) => {
  if (e.target.closest('[data-close]')) {
    closeModal(e.target.closest('.modal-overlay').id);
    return;
  }

  // Clicking the backdrop (but not the dialog) dismisses the modal.
  if (e.target.classList.contains('modal-overlay')) {
    closeModal(e.target.id);
    return;
  }

  const tab = e.target.closest('.tab');
  if (tab) {
    showView(tab.dataset.view);
    return;
  }

  const trigger = e.target.closest('[data-action]');
  if (!trigger) return;

  const run = ACTIONS[trigger.dataset.action];
  if (!run) return;
  // Buttons sit inside the clickable project header; don't also toggle it.
  e.stopPropagation();
  Promise.resolve(run({ ...trigger.dataset })).catch(handle);
});

document.addEventListener('keydown', (e) => {
  if (e.key !== 'Escape') return;
  for (const overlay of document.querySelectorAll('.modal-overlay')) overlay.hidden = true;
});

window.addEventListener('hashchange', () => {
  if ($('app').hidden) return;
  showView(location.hash.slice(1));
});

/* ------------------------------------------------------------------ boot */

(async function boot() {
  setInterval(checkHealth, 30000);

  if (!state.token) {
    signOut();
    return;
  }
  try {
    await signIn(state.token);
  } catch (err) {
    // A stored token that no longer works should not strand the user on a
    // blank screen — fall back to the login gate.
    signOut(err.status === 401 || err.status === 403 ? 'Stored token is no longer valid.' : err.message);
  }
})();
