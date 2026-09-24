'use strict';

/* mailsync configuration generator: interface only.
   The YAML model lives in config.js and the text in strings.js. Nothing here
   touches the network, and no field ever holds a password. */

const C = MailsyncConfig;
const STRINGS = MailsyncStrings;
const LANG_KEY = 'mailsync.lang';

function detectLang() {
  const tags = navigator.languages && navigator.languages.length
    ? navigator.languages : [navigator.language || 'es'];
  // Spanish unless the browser clearly prefers English first.
  return tags[0] && tags[0].toLowerCase().startsWith('en') ? 'en' : 'es';
}

// The browser's own preference is the starting point; an explicit choice
// from the menu overrides it from then on.
function storedLang() {
  try {
    const saved = localStorage.getItem(LANG_KEY);
    return saved === 'es' || saved === 'en' ? saved : null;
  } catch {
    return null;
  }
}

let lang = storedLang() || detectLang();

const t = (key, ...args) => {
  let s = STRINGS[lang][key];
  if (s === undefined) s = key;
  args.forEach((a) => { s = s.replace('%s', a); });
  return s;
};

const state = C.newState('');
const view = { name: 'account', account: 0 };

/* ---------- small DOM helpers ---------- */

const $ = (id) => document.getElementById(id);

function el(tag, props, ...children) {
  const node = document.createElement(tag);
  Object.entries(props || {}).forEach(([k, v]) => {
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k === 'html') node.innerHTML = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2).toLowerCase(), v);
    else if (v !== null && v !== undefined && v !== false) node.setAttribute(k, v);
  });
  children.flat().forEach((c) => c && node.append(c));
  return node;
}

function field(labelText, input, hint) {
  const wrap = el('div', {}, el('label', { text: labelText }), input);
  if (hint) wrap.append(el('div', { class: 'hint', text: hint }));
  return wrap;
}

function textInput(value, placeholder, onInput) {
  const node = el('input', { type: 'text', placeholder: placeholder || null });
  node.value = value || '';
  node.addEventListener('input', () => { onInput(node.value); afterEdit(); });
  return node;
}

function select(options, current, onChange) {
  const node = el('select', {});
  options.forEach(([value, label]) => {
    const option = el('option', { value, text: label });
    if (value === current) option.selected = true;
    node.append(option);
  });
  node.addEventListener('change', () => onChange(node.value));
  return node;
}

/* ---------- state helpers ---------- */

const yamlText = () => ({ promptComment: t('promptComment') });
const secretsText = () => ({ noVars: t('noVars'), secretsHeader: t('secretsHeader') });

function errorsFor() {
  return C.validate(state).map(({ code, args }) => {
    const shown = args.map((a) => (a === 'source' ? t('source') : a === 'dest' ? t('dest') : a));
    return { code, args, text: t('err_' + code, ...shown) };
  });
}

// accountHasErrors keys errors back to the account they came from, so the
// sidebar can flag which one needs attention without opening it.
function accountHasErrors(index) {
  const acc = state.accounts[index];
  const label = acc.name.trim() || '#' + (index + 1);
  return errorsFor().some((e) => e.args.length > 0 && String(e.args[0]) === label);
}

function accountLabel(index) {
  return state.accounts[index].name.trim() || t('untitled');
}

/* ---------- top bar ---------- */

function renderTopbar() {
  $('brand-sub').textContent = t('brandSub');
  const errors = errorsFor();
  const chip = $('status-chip');
  if (errors.length === 0) {
    chip.className = 'chip ok';
    chip.textContent = '✓ ' + t('statusOk');
  } else {
    chip.className = 'chip bad';
    chip.textContent = errors.length === 1 ? t('statusOneError') : t('statusErrors', errors.length);
  }
  $('lang-select').value = lang;
}

function setLang(chosen) {
  lang = chosen;
  try { localStorage.setItem(LANG_KEY, chosen); } catch { /* ignore */ }
  document.documentElement.lang = lang;
  render();
}

/* ---------- sidebar ---------- */

function sideItem(label, isCurrent, onClick, options) {
  const opts = options || {};
  const item = el('button', {
    class: 'side-item' + (opts.class ? ' ' + opts.class : ''),
    'aria-current': String(!!isCurrent),
    onclick: onClick,
  }, el('span', { class: 'grow', text: label }));
  if (opts.flag) item.append(el('span', { class: 'dot', title: t('hasErrors') }));
  return item;
}

function renderSidebar() {
  const bar = $('sidebar');
  bar.textContent = '';

  bar.append(el('div', { class: 'side-title', text: t('sideAccounts') }));
  state.accounts.forEach((_, i) => {
    bar.append(sideItem(
      accountLabel(i),
      view.name === 'account' && view.account === i,
      () => { view.name = 'account'; view.account = i; render(); },
      { flag: accountHasErrors(i) }
    ));
  });
  bar.append(sideItem('+ ' + t('sideAdd'), false, () => {
    state.accounts.push(C.newAccount(''));
    view.name = 'account';
    view.account = state.accounts.length - 1;
    render();
  }, { class: 'add' }));

  bar.append(el('div', { class: 'side-sep' }));
  bar.append(el('div', { class: 'side-title', text: t('sideSettings') }));
  bar.append(sideItem(t('logTitle'), view.name === 'log', () => { view.name = 'log'; render(); }));

  bar.append(el('div', { class: 'side-sep' }));
  bar.append(el('div', { class: 'side-title', text: t('sideOutput') }));
  bar.append(sideItem('config.yaml', view.name === 'yaml', () => { view.name = 'yaml'; render(); }));
  bar.append(sideItem('secrets.env', view.name === 'secrets', () => { view.name = 'secrets'; render(); }));

  bar.append(el('div', { class: 'side-sep' }));
  bar.append(el('div', { class: 'side-title', text: t('sideFile') }));
  bar.append(sideItem(t('sideLoad'), view.name === 'import', () => { view.name = 'import'; render(); }));
  bar.append(sideItem(t('sideExport'), false, exportAll));
}

/* ---------- account view ---------- */

function renderEndpoint(acc, key) {
  const ep = acc[key];
  const box = el('div', {}, el('h3', { text: key === 'source' ? t('source') : t('dest') }));

  const grid = el('div', { class: 'grid' });
  grid.append(field(t('preset'),
    select([['custom', t('presetCustom')], ['gmail', t('presetGmail')]], ep.preset, (v) => {
      ep.preset = v;
      render();
    }),
    ep.preset === 'gmail' ? t('presetGmailHint') : ''));

  if (ep.preset !== 'gmail') {
    grid.append(field(t('host'), textInput(ep.host, 'mail.midominio.com', (v) => { ep.host = v; })));

    const port = el('input', { type: 'number', min: '1', max: '65535' });
    port.value = ep.port;
    port.addEventListener('input', () => { ep.port = port.value; afterEdit(); });
    grid.append(field(t('port'), port));

    grid.append(field(t('security'),
      select([['tls', 'TLS (993)'], ['starttls', 'STARTTLS (143)']], ep.tls, (v) => { ep.tls = v; afterEdit(); })));
  }

  const userHint = ep.preset === 'gmail' ? 'cuenta@gmail.com'
    : (key === 'source' ? 'usuario@midominio.com' : 'usuario@destino.com');
  grid.append(field(t('user'), textInput(ep.user, userHint, (v) => { ep.user = v; })));
  box.append(grid);

  // Password: a variable name, or nothing at all. Never a password field.
  const pw = el('div', { style: 'margin-top:.7rem' }, el('label', { text: t('password') }));
  const radios = el('div', { class: 'radios' });
  const group = 'pw-' + view.account + '-' + key;
  [['var', t('pwVar')], ['prompt', t('pwPrompt')], ['literal', t('pwLiteral')]].forEach(([mode, label]) => {
    const radio = el('input', { type: 'radio', name: group });
    radio.checked = ep.pwMode === mode;
    radio.addEventListener('change', () => { ep.pwMode = mode; render(); });
    radios.append(el('label', {}, radio, document.createTextNode(label)));
  });
  pw.append(radios);

  if (ep.pwMode === 'var') {
    const hint = key === 'source' ? 'ORIGEN_PASS' : (ep.preset === 'gmail' ? 'GMAIL_PASS' : 'DESTINO_PASS');
    pw.append(el('div', { class: 'grid', style: 'margin-top:.45rem' },
      field(t('pwVarName'), textInput(ep.pwVar, hint, (v) => { ep.pwVar = v.toUpperCase(); }))));
  } else if (ep.pwMode === 'literal') {
    pw.append(literalPassword(ep));
  } else {
    pw.append(el('div', { class: 'hint', text: t('pwPromptHint') }));
  }
  box.append(pw);

  box.append(el('div', { class: 'grid', style: 'margin-top:.7rem' },
    field(t('fingerprint'), textInput(ep.fingerprint, '', (v) => { ep.fingerprint = v; }), t('fingerprintHint'))));
  return box;
}

// literalPassword is the opt-in field for typing a password straight into the
// config. It is masked, kept away from the browser's password manager, and
// never persisted: the value lives in memory until the tab is closed.
function literalPassword(ep) {
  const input = el('input', {
    type: 'password',
    autocomplete: 'off',
    autocapitalize: 'off',
    autocorrect: 'off',
    spellcheck: 'false',
    'data-lpignore': 'true',
    'data-1p-ignore': 'true',
  });
  input.value = ep.pwValue || '';
  input.addEventListener('input', () => { ep.pwValue = input.value; afterEdit(); });

  const toggle = el('button', {
    class: 'btn', type: 'button', text: t('show'),
    onclick: () => {
      const masked = input.type === 'password';
      input.type = masked ? 'text' : 'password';
      toggle.textContent = masked ? t('hide') : t('show');
    },
  });

  const row = el('div', { class: 'row', style: 'margin-top:.2rem; flex-wrap:nowrap' }, input, toggle);
  input.style.flex = '1';

  return el('div', { style: 'margin-top:.45rem' },
    el('label', { text: t('pwValueLabel') }),
    row);
}

function renderFolders(acc) {
  const box = el('div', {}, el('h3', { text: t('folders') }));
  acc.folders.forEach((folder, i) => {
    box.append(el('div', { class: 'folder-row' },
      textInput(folder.from, t('folderFrom'), (v) => { folder.from = v; }),
      el('span', { class: 'arrow', text: '→' }),
      textInput(folder.to, t('folderTo'), (v) => { folder.to = v; }),
      el('button', {
        class: 'icon', title: t('remove'), text: '✕',
        onclick: () => { acc.folders.splice(i, 1); render(); },
      })));
  });
  box.append(el('div', { class: 'hint', text: t('folderHint') }));
  box.append(el('button', {
    class: 'btn', text: t('addFolder'), style: 'margin-top:.5rem',
    onclick: () => { acc.folders.push({ from: '', to: '' }); render(); },
  }));
  return box;
}

function renderAccountView(main) {
  const acc = state.accounts[view.account];
  if (!acc) { view.name = 'yaml'; return renderMain(); }

  const head = el('div', { class: 'out-head' },
    el('h2', { text: accountLabel(view.account) }));
  if (state.accounts.length > 1) {
    head.append(el('button', {
      class: 'btn', text: t('removeAccount'),
      onclick: () => {
        state.accounts.splice(view.account, 1);
        view.account = Math.max(0, view.account - 1);
        render();
      },
    }));
  }
  main.append(head);

  const nameInput = textInput(acc.name, 'trabajo', (v) => {
    acc.name = v;
    renderSidebar();
  });
  main.append(el('div', { class: 'grid' },
    field(t('name'), nameInput, t('nameHint')),
    field(t('interval'), textInput(acc.interval, '5m', (v) => { acc.interval = v; }), t('intervalHint'))));

  main.append(renderEndpoint(acc, 'source'));
  main.append(renderEndpoint(acc, 'dest'));
  main.append(renderFolders(acc));
}

/* ---------- other views ---------- */

function renderLogView(main) {
  main.append(el('h2', { text: t('logTitle') }));
  main.append(el('p', { class: 'sub', text: t('logSub') }));

  const maxSize = textInput(state.log.maxSize, '5MB', (v) => { state.log.maxSize = v; });
  const keep = el('input', { type: 'number', min: '0', max: '100' });
  keep.value = state.log.keep;
  keep.addEventListener('input', () => { state.log.keep = keep.value; afterEdit(); });

  main.append(el('div', { class: 'grid' },
    field(t('logMax'), maxSize, t('logMaxHint')),
    field(t('logKeep'), keep, t('logKeepHint'))));
}

function outputView(main, title, subtitle, content, filename) {
  main.append(el('h2', { text: title }));
  main.append(el('p', { class: 'sub', html: subtitle }));

  const copyBtn = el('button', { class: 'btn', text: t('copy') });
  copyBtn.addEventListener('click', () => copyToClipboard(content, copyBtn));
  main.append(el('div', { class: 'out-head' },
    el('span', {}),
    el('div', { class: 'row' },
      copyBtn,
      el('button', {
        class: 'btn', text: t('download'),
        onclick: () => download(filename, content),
      }))));
  main.append(el('pre', { text: content }));
}

function renderImportView(main) {
  main.append(el('h2', { text: t('importTitle') }));
  main.append(el('p', { class: 'sub', text: t('importHint') }));

  const status = el('div', { class: 'hint' });
  const area = el('textarea', { spellcheck: 'false', placeholder: 'accounts:\n  - name: trabajo\n    ...' });

  const load = (text) => {
    try {
      const loaded = C.stateFromYAML(text);
      state.accounts = loaded.accounts;
      state.log = loaded.log;
      view.name = 'account';
      view.account = 0;
      render();
    } catch (err) {
      status.textContent = t('importErr') + err.message;
      status.style.color = 'var(--danger)';
    }
  };

  const fileInput = $('file-input');
  fileInput.onchange = () => {
    const file = fileInput.files && fileInput.files[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => { area.value = String(reader.result); load(area.value); };
    reader.readAsText(file);
    fileInput.value = '';
  };

  main.append(el('div', { class: 'row', style: 'margin-bottom:.7rem' },
    el('button', { class: 'btn', text: t('chooseFile'), onclick: () => fileInput.click() }),
    el('span', { class: 'hint', text: t('orPaste') })));
  main.append(area);
  main.append(el('div', { class: 'row', style: 'margin-top:.6rem' },
    el('button', { class: 'btn primary', text: t('importBtn'), onclick: () => load(area.value) }),
    status));
}

function renderErrors(main) {
  const errors = errorsFor();
  if (errors.length === 0) return;
  const box = el('div', { class: 'errors' }, el('div', { text: t('errTitle') }));
  const list = el('ul');
  errors.forEach((e) => list.append(el('li', { text: e.text })));
  box.append(list);
  main.append(box);
}

function renderMain() {
  const main = $('main-inner');
  main.textContent = '';

  switch (view.name) {
    case 'account': renderAccountView(main); break;
    case 'log': renderLogView(main); break;
    case 'yaml':
      renderErrors(main);
      outputView(main, 'config.yaml', t('outHint'), C.buildYAML(state, yamlText()), 'config.yaml');
      break;
    case 'secrets':
      outputView(main, 'secrets.env', t('secretsHint'), C.buildSecrets(state, secretsText()), 'secrets.env');
      break;
    case 'import': renderImportView(main); break;
    default: break;
  }

  main.append(el('footer', {},
    el('div', { text: t('footerNote') }),
    el('div', {},
      document.createTextNode(t('footer') + ' '),
      el('a', { href: 'https://github.com/thisjrodriguez/mailSync', text: 'github.com/thisjrodriguez/mailSync' }))));
}

/* afterEdit keeps typing cheap: only the parts that can change while a field
   has focus are redrawn, so the caret is never thrown out of an input. */
function afterEdit() {
  renderTopbar();
  state.accounts.forEach((_, i) => {
    const item = $('sidebar').querySelectorAll('.side-item')[i];
    if (!item) return;
    item.querySelector('.grow').textContent = accountLabel(i);
    const flagged = accountHasErrors(i);
    const dot = item.querySelector('.dot');
    if (flagged && !dot) item.append(el('span', { class: 'dot', title: t('hasErrors') }));
    if (!flagged && dot) dot.remove();
  });
}

function render() {
  document.documentElement.lang = lang;
  renderTopbar();
  renderSidebar();
  renderMain();
}

/* ---------- file helpers ---------- */

function download(filename, content) {
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = el('a', { href: url, download: filename });
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function exportAll() {
  download('config.yaml', C.buildYAML(state, yamlText()));
  if (C.variableNames(state).length > 0) {
    download('secrets.env', C.buildSecrets(state, secretsText()));
  }
}

async function copyToClipboard(text, button) {
  const original = button.textContent;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const area = el('textarea', {});
    area.value = text;
    document.body.append(area);
    area.select();
    document.execCommand('copy');
    area.remove();
  }
  button.textContent = t('copied');
  setTimeout(() => { button.textContent = original; }, 1200);
}

/* ---------- wiring ---------- */

$('lang-select').addEventListener('change', (e) => setLang(e.target.value));

render();
