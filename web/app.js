// pgmanager web UI.
//
// Served by `pgmanager serve` from ./web on the same origin as /api, so the
// bearer token never crosses an origin boundary. The server sends a strict CSP
// (script-src 'self'; style-src 'self'), which is why there is no inline
// script, no inline style and no inline event handler anywhere in this UI —
// everything is wired up through delegated listeners below.

const API = '/api';

const state = {
  // No credential lives here: the session is an HttpOnly cookie the browser
  // attaches itself, which is why script can neither read nor leak it.
  session: null,
  whoami: null,
  projects: [],
  // project name -> array of databases, populated lazily on expand.
  databases: {},
  expanded: new Set(),
  tokens: [],
  devices: [],
  // Data explorer: which database is open, its table list, and the page of
  // rows currently on screen.
  explore: {
    project: '', env: '', tables: [], schema: '', table: '', page: null, offset: 0,
  },
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
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  let res;
  try {
    res = await fetch(API + path, {
      method,
      headers,
      credentials: 'same-origin',
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

// Any 401/403 mid-session means the session expired or the account was
// removed underneath us; go back to the login gate rather than showing
// empty views.
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
          class: 'ghost small', type: 'button', 'data-action': 'explore-database',
          'data-project': projectName, 'data-env': segment, text: 'Explore',
        }),
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

/* ---------------------------------------------------------- data explorer */

// The explorer is a page per database: it lists that database's tables and
// lets you page through and edit rows. The server connects as the database's
// own role, so what is reachable here is exactly what those credentials allow.

const ROW_LIMIT = 50;

function explorePath(suffix = '') {
  const ex = state.explore;
  return `/projects/${encodeURIComponent(ex.project)}/databases/${encodeURIComponent(ex.env)}${suffix}`;
}

function rowsPath(extra = '') {
  const ex = state.explore;
  const q = new URLSearchParams({ schema: ex.schema });
  return `${explorePath(`/tables/${encodeURIComponent(ex.table)}/rows`)}?${q}${extra}`;
}

// Values arrive already JSON-safe from the server; objects and arrays (json,
// arrays, composite types) are shown as their JSON text.
function cellText(v) {
  if (v === null || v === undefined) return 'NULL';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

function primaryKeyColumns() {
  const page = state.explore.page;
  return page ? page.columns.filter((c) => c.primary_key) : [];
}

// The key that addresses one row for update/delete. The server insists it
// names exactly the primary key, so a table without one is read-only here.
function rowKey(row) {
  const key = {};
  for (const col of primaryKeyColumns()) key[col.name] = row[col.name];
  return key;
}

async function loadExplore(route) {
  const ex = state.explore;
  if (ex.project !== route.project || ex.env !== route.env) {
    Object.assign(ex, {
      project: route.project, env: route.env, tables: [], schema: '', table: '', page: null, offset: 0,
    });
  }
  $('exploreTitle').textContent = `Explore ${route.project} / ${route.env}`;
  $('exploreError').hidden = true;

  const res = await api('GET', explorePath('/tables'));
  ex.tables = (res && res.tables) || [];
  renderTableList();

  if (ex.table) await loadRows();
  else renderRows();
}

function renderTableList() {
  const ex = state.explore;
  const list = $('exploreTableList');
  if (ex.tables.length === 0) {
    list.replaceChildren(el('p', { class: 'empty', text: 'No tables in this database.' }));
    return;
  }
  list.replaceChildren(...ex.tables.map((t) => {
    const selected = t.schema === ex.schema && t.name === ex.table;
    // Qualify the label only when a non-public schema is in play, so the
    // common case stays uncluttered.
    const label = t.schema === 'public' ? t.name : `${t.schema}.${t.name}`;
    return el('button', {
      type: 'button',
      class: `ghost small table-item${selected ? ' active' : ''}`,
      'data-action': 'select-table',
      'data-schema': t.schema,
      'data-table': t.name,
      text: label,
    });
  }));
}

async function selectTable(schema, table) {
  const ex = state.explore;
  ex.schema = schema;
  ex.table = table;
  ex.offset = 0;
  renderTableList();
  await loadRows();
}

async function loadRows() {
  const ex = state.explore;
  $('exploreError').hidden = true;
  try {
    ex.page = await api('GET', rowsPath(`&limit=${ROW_LIMIT}&offset=${ex.offset}`));
  } catch (err) {
    if (err.status === 401 || err.status === 403) throw err;
    ex.page = null;
    showError('exploreError', err);
  }
  renderRows();
}

function renderRows() {
  const ex = state.explore;
  const container = $('exploreRows');
  const pager = $('explorePager');

  $('exploreTableName').textContent = ex.table
    ? (ex.schema === 'public' ? ex.table : `${ex.schema}.${ex.table}`)
    : 'No table selected';
  $('exploreInsert').hidden = !ex.table;

  if (!ex.table) {
    container.replaceChildren(el('p', { class: 'empty', text: 'Pick a table to browse its rows.' }));
    pager.hidden = true;
    return;
  }
  if (!ex.page) {
    container.replaceChildren();
    pager.hidden = true;
    return;
  }

  const { columns, rows } = ex.page;
  const editable = primaryKeyColumns().length > 0;

  const nodes = [];
  if (!editable) {
    nodes.push(el('p', {
      class: 'muted',
      text: 'This table has no primary key, so rows cannot be edited or deleted here.',
    }));
  }

  if (rows.length === 0) {
    nodes.push(el('p', { class: 'empty', text: 'No rows.' }));
  } else {
    const header = el('tr', {}, columns
      .map((c) => el('th', { text: c.primary_key ? `${c.name} 🔑` : c.name }))
      .concat([el('th', {})]));

    const body = rows.map((row, i) => {
      const cells = columns.map((c) => {
        const value = row[c.name];
        const isNull = value === null || value === undefined;
        return el('td', { class: isNull ? 'muted' : 'mono', text: cellText(value) });
      });
      cells.push(el('td', { class: 'actions' }, editable ? [
        el('button', {
          class: 'ghost small', type: 'button', 'data-action': 'row-edit', 'data-index': i, text: 'Edit',
        }),
        el('button', {
          class: 'danger small', type: 'button', 'data-action': 'row-delete', 'data-index': i, text: 'Delete',
        }),
      ] : []));
      return el('tr', {}, cells);
    });

    nodes.push(el('div', { class: 'table-scroll' }, [
      el('table', {}, [el('thead', {}, [header]), el('tbody', {}, body)]),
    ]));
  }

  container.replaceChildren(...nodes);

  const { total, offset } = ex.page;
  const first = total === 0 ? 0 : offset + 1;
  const last = offset + rows.length;
  $('exploreRange').textContent = `${first}–${last} of ${total}`;
  pager.querySelector('[data-action="rows-prev"]').disabled = offset === 0;
  pager.querySelector('[data-action="rows-next"]').disabled = last >= total;
  pager.hidden = false;
}

async function pageRows(delta) {
  const ex = state.explore;
  const next = ex.offset + delta * ROW_LIMIT;
  if (next < 0) return;
  ex.offset = next;
  await loadRows();
}

/* ------------------------------------------------------ row insert/edit */

// Built fresh each time the modal opens: one control set per column, kept
// here so the submit handler can read them back.
let rowFields = [];
let rowMode = 'insert';
let rowOriginal = null;

// The string form of a value as it goes into the editor. Round-trips through
// the same text the cell shows, so an untouched field submits unchanged.
function inputValue(v) {
  if (v === null || v === undefined) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

function rowField(col, value, mode) {
  const input = el('input', {
    type: 'text', value: inputValue(value), autocomplete: 'off', spellcheck: 'false',
  });

  const controls = [input];
  const field = { name: col.name, input, nullBox: null, defaultBox: null };

  const sync = () => {
    input.disabled = (field.nullBox && field.nullBox.checked)
      || (field.defaultBox && field.defaultBox.checked);
  };

  // Inserting: a column with a default (a serial primary key, a timestamp)
  // should normally be left to Postgres, so offer that as the default choice.
  if (mode === 'insert' && col.default) {
    field.defaultBox = el('input', { type: 'checkbox', checked: true });
    field.defaultBox.addEventListener('change', sync);
    controls.push(el('label', { class: 'inline' }, [field.defaultBox, ' default']));
  }

  if (col.nullable) {
    field.nullBox = el('input', { type: 'checkbox', checked: value === null || value === undefined });
    field.nullBox.addEventListener('change', sync);
    controls.push(el('label', { class: 'inline' }, [field.nullBox, ' NULL']));
  }

  sync();
  rowFields.push(field);

  const type = el('span', { class: 'muted', text: ` ${col.type}${col.primary_key ? ' 🔑' : ''}` });
  return el('div', { class: 'field' }, [
    el('label', {}, [col.name, type]),
    el('div', { class: 'row' }, controls),
  ]);
}

function openRowModal(mode, row) {
  const page = state.explore.page;
  if (!page) return;

  rowFields = [];
  rowMode = mode;
  rowOriginal = row || null;

  $('rowTitle').textContent = mode === 'insert' ? 'Insert row' : 'Edit row';
  $('rowSubmit').textContent = mode === 'insert' ? 'Insert' : 'Save';
  $('rowError').hidden = true;
  $('rowFields').replaceChildren(
    ...page.columns.map((col) => rowField(col, row ? row[col.name] : null, mode)),
  );
  openModal('modal-row');
}

// Reads the editor back into the JSON body. On edit, only fields the operator
// actually touched are sent, so an UPDATE never rewrites columns it did not
// need to (and never fights a database-side default or trigger).
function collectRowValues() {
  const values = {};
  for (const field of rowFields) {
    if (field.defaultBox && field.defaultBox.checked) continue;
    const value = field.nullBox && field.nullBox.checked ? null : field.input.value;

    if (rowMode === 'edit') {
      const before = rowOriginal[field.name];
      const beforeIsNull = before === null || before === undefined;
      if (value === null && beforeIsNull) continue;
      if (value !== null && !beforeIsNull && value === inputValue(before)) continue;
    }
    values[field.name] = value;
  }
  return values;
}

$('formRow').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('rowError').hidden = true;

  const values = collectRowValues();
  try {
    if (rowMode === 'insert') {
      await api('POST', rowsPath(), { values });
      toast('Row inserted');
    } else {
      if (Object.keys(values).length === 0) {
        closeModal('modal-row');
        return;
      }
      await api('PATCH', rowsPath(), { key: rowKey(rowOriginal), values });
      toast('Row updated');
    }
    closeModal('modal-row');
    await loadRows();
  } catch (err) {
    if (err.status === 401 || err.status === 403) handle(err);
    else showError('rowError', err);
  }
});

/* ------------------------------------------------------------ views/nav */

// TABS are the views with a nav button; VIEWS additionally covers `explore`,
// which is always about one specific database and so is only reachable from a
// database row rather than from the nav.
const TABS = ['projects', 'tokens', 'devices', 'maintenance'];
const VIEWS = TABS.concat(['explore']);

// Loaders are best-effort: a non-admin token may legitimately lack the
// `tokens` scope, in which case that view just reports the failure.
const LOADERS = {
  projects: loadProjects,
  tokens: loadTokens,
  devices: loadDevices,
  maintenance: renderWhoami,
  explore: loadExplore,
};

// Routes are hashes. Tabs are bare names; the explorer carries its target in
// the hash (#explore/myapp/pr_42) so the page survives a reload and can be
// linked to directly.
function parseRoute(hash) {
  const parts = String(hash).replace(/^#/, '').split('/').filter(Boolean).map(decodeURIComponent);
  if (parts[0] === 'explore' && parts.length >= 3) {
    return { name: 'explore', project: parts[1], env: parts[2] };
  }
  return { name: TABS.includes(parts[0]) ? parts[0] : 'projects' };
}

function routeHash(route) {
  if (route.name === 'explore') {
    return `explore/${encodeURIComponent(route.project)}/${encodeURIComponent(route.env)}`;
  }
  return route.name;
}

async function showView(target) {
  const route = typeof target === 'string' ? parseRoute(target) : target;
  for (const v of VIEWS) $(`view-${v}`).hidden = v !== route.name;
  for (const tab of document.querySelectorAll('.tab')) {
    tab.classList.toggle('active', tab.dataset.view === route.name);
  }
  const hash = routeHash(route);
  if (location.hash.slice(1) !== hash) location.hash = hash;

  try {
    await LOADERS[route.name](route);
  } catch (err) {
    handle(err);
  }
}

function renderWhoami() {
  const dl = $('whoamiDetail');
  const who = state.whoami || { token_prefix: '—', scopes: [] };
  const rows = [
    el('dt', { text: who.email ? 'Signed in as' : 'Token' }),
    el('dd', { text: who.email || who.token_prefix }),
    el('dt', { text: 'Scopes' }),
    el('dd', { text: (who.scopes || []).join('\n') || 'none' }),
  ];
  if (state.session && state.session.expires_at) {
    rows.push(el('dt', { text: 'Session ends' }), el('dd', { text: fmtDate(state.session.expires_at) }));
  }
  dl.replaceChildren(...rows);
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

// enterApp is called once the session cookie is known good.
async function enterApp() {
  // whoami is the cheapest authenticated endpoint and every principal can
  // call it — so it is the right probe for "is this session good?"
  state.whoami = await api('GET', '/auth/whoami');

  $('login').hidden = true;
  $('app').hidden = false;
  $('whoamiPrefix').textContent = state.whoami.email || state.whoami.token_prefix;

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

// signOut clears local state and shows the gate. The server-side session is
// dropped separately by `logout` — this is also the path taken when the
// session has already stopped working, where there is nothing to drop.
function signOut(message) {
  state.session = null;
  state.whoami = null;
  state.projects = [];
  state.databases = {};
  state.expanded.clear();
  state.tokens = [];
  state.devices = [];

  $('app').hidden = true;
  $('login').hidden = false;
  $('loginPassword').value = '';
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
  const email = $('loginEmail').value.trim();
  const password = $('loginPassword').value;
  $('loginError').hidden = true;
  if (!email || !password) {
    showError('loginError', new Error('Email and password are required.'));
    return;
  }
  const submit = $('loginSubmit');
  submit.disabled = true;
  try {
    state.session = await api('POST', '/auth/login', { email, password });
    $('loginPassword').value = '';
    await enterApp();
  } catch (err) {
    // The server gives one message for wrong password, unknown address and
    // disabled account, on purpose — don't embellish it here.
    showError('loginError', err);
  } finally {
    submit.disabled = false;
  }
});

$('logout').addEventListener('click', async () => {
  try {
    await api('POST', '/auth/logout');
  } catch { /* the session may already be gone; the gate is the point */ }
  signOut();
});

$('formPassword').addEventListener('submit', async (e) => {
  e.preventDefault();
  $('pwError').hidden = true;
  const current = $('pwCurrent').value;
  const next = $('pwNew').value;
  if (!current || !next) {
    showError('pwError', new Error('Both fields are required.'));
    return;
  }
  try {
    await api('POST', '/auth/password', { current, new: next });
    // The server drops every session on change, including this one.
    signOut('Password changed — sign in again.');
  } catch (err) {
    showError('pwError', err);
  }
});

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

  'explore-database': (data) => showView({ name: 'explore', project: data.project, env: data.env }),

  'explore-back': () => showView('projects'),

  'select-table': (data) => selectTable(data.schema, data.table),

  'rows-prev': () => pageRows(-1),

  'rows-next': () => pageRows(1),

  'row-new': () => openRowModal('insert', null),

  'row-edit': (data) => openRowModal('edit', state.explore.page.rows[Number(data.index)]),

  'row-delete': (data) => {
    const row = state.explore.page.rows[Number(data.index)];
    const key = rowKey(row);
    const label = Object.entries(key).map(([k, v]) => `${k}=${cellText(v)}`).join(', ');
    return confirmAction(
      'Delete row?',
      `Row ${label} will be deleted from ${state.explore.table}. This cannot be undone.`,
      async () => {
        await api('DELETE', rowsPath(), { key });
        toast('Row deleted');
        await loadRows();
      },
    );
  },

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

  // The cookie is HttpOnly, so the only way to know whether we are signed in
  // is to ask. A 401 here is the normal first-visit case, not an error.
  try {
    await enterApp();
  } catch (err) {
    signOut(err.status === 401 || err.status === 403 ? '' : err.message);
  }
})();
