'use strict';

/* mailsync config generator.
   Everything runs in the page: no network, no passwords. The form produces a
   config.yaml plus a matching secrets.env template. */

const STRINGS = MailsyncStrings;

let lang = (navigator.language || 'es').toLowerCase().startsWith('en') ? 'en' : 'es';
const t = (key, ...args) => {
  let s = STRINGS[lang][key] || key;
  args.forEach((a) => { s = s.replace('%s', a); });
  return s;
};

const C = MailsyncConfig;
const DEFAULT_INTERVAL = C.DEFAULT_INTERVAL;

function yamlText() {
  return { promptComment: t('promptComment') };
}

function secretsText() {
  return { noVars: t('noVars'), secretsHeader: t('secretsHeader') };
}

// Validation lives in config.js and returns codes; the sentences live here.
function errorMessages() {
  return C.validate(state).map(({ code, args }) => {
    const translated = args.map((a) => (a === 'source' ? t('source') : a === 'dest' ? t('dest') : a));
    return t('err_' + code, ...translated);
  });
}

const state = C.newState('');

/* ---------- rendering ---------- */

const $ = (id) => document.getElementById(id);

function field(labelText, input, hint) {
  const wrap = document.createElement('div');
  const label = document.createElement('label');
  label.textContent = labelText;
  wrap.append(label, input);
  if (hint) {
    const h = document.createElement('div');
    h.className = 'hint';
    h.textContent = hint;
    wrap.append(h);
  }
  return wrap;
}

function textInput(value, placeholder, onInput) {
  const el = document.createElement('input');
  el.type = 'text';
  el.value = value || '';
  if (placeholder) el.placeholder = placeholder;
  el.addEventListener('input', () => { onInput(el.value); refreshOutput(); });
  return el;
}

function renderEndpoint(acc, key, side) {
  const ep = acc[key];
  const box = document.createElement('div');
  const title = document.createElement('h3');
  title.textContent = side;
  box.append(title);

  const preset = document.createElement('select');
  [['custom', t('presetCustom')], ['gmail', t('presetGmail')]].forEach(([v, label]) => {
    const o = document.createElement('option');
    o.value = v; o.textContent = label; o.selected = ep.preset === v;
    preset.append(o);
  });
  preset.addEventListener('change', () => { ep.preset = preset.value; render(); });

  const grid = document.createElement('div');
  grid.className = 'grid';
  grid.append(field(t('preset'), preset, ep.preset === 'gmail' ? t('presetGmailHint') : ''));

  if (ep.preset !== 'gmail') {
    grid.append(field(t('host'), textInput(ep.host, 'mail.midominio.com', (v) => { ep.host = v; })));
    const port = document.createElement('input');
    port.type = 'number'; port.value = ep.port; port.min = '1'; port.max = '65535';
    port.addEventListener('input', () => { ep.port = port.value; refreshOutput(); });
    grid.append(field(t('port'), port));

    const tls = document.createElement('select');
    [['tls', 'TLS (993)'], ['starttls', 'STARTTLS (143)']].forEach(([v, label]) => {
      const o = document.createElement('option');
      o.value = v; o.textContent = label; o.selected = ep.tls === v;
      tls.append(o);
    });
    tls.addEventListener('change', () => { ep.tls = tls.value; refreshOutput(); });
    grid.append(field(t('security'), tls));
  }

  grid.append(field(t('user'), textInput(ep.user, 'usuario@midominio.com', (v) => { ep.user = v; })));
  box.append(grid);

  // Password: a variable name or nothing at all. Never a password field.
  const pwWrap = document.createElement('div');
  pwWrap.style.marginTop = '.75rem';
  const pwLabel = document.createElement('label');
  pwLabel.textContent = t('password');
  const radios = document.createElement('div');
  radios.className = 'radios';
  const groupName = 'pw-' + acc.name + '-' + key + '-' + Math.random().toString(36).slice(2, 7);
  [['var', t('pwVar')], ['prompt', t('pwPrompt')]].forEach(([mode, label]) => {
    const l = document.createElement('label');
    const r = document.createElement('input');
    r.type = 'radio'; r.name = groupName; r.checked = ep.pwMode === mode;
    r.addEventListener('change', () => { ep.pwMode = mode; render(); });
    l.append(r, document.createTextNode(label));
    radios.append(l);
  });
  pwWrap.append(pwLabel, radios);

  if (ep.pwMode === 'var') {
    const varGrid = document.createElement('div');
    varGrid.className = 'grid';
    varGrid.style.marginTop = '.5rem';
    varGrid.append(field(t('pwVarName'), textInput(ep.pwVar, 'TRABAJO_PASS', (v) => { ep.pwVar = v.toUpperCase(); })));
    pwWrap.append(varGrid);
  } else {
    const h = document.createElement('div');
    h.className = 'hint'; h.textContent = t('pwPromptHint');
    pwWrap.append(h);
  }
  box.append(pwWrap);

  const fpGrid = document.createElement('div');
  fpGrid.className = 'grid';
  fpGrid.style.marginTop = '.75rem';
  fpGrid.append(field(t('fingerprint'),
    textInput(ep.fingerprint, '', (v) => { ep.fingerprint = v; }), t('fingerprintHint')));
  box.append(fpGrid);

  return box;
}

function renderFolders(acc) {
  const box = document.createElement('div');
  box.style.marginTop = '1rem';
  const title = document.createElement('h3');
  title.textContent = t('folders');
  box.append(title);

  acc.folders.forEach((folder, i) => {
    const row = document.createElement('div');
    row.className = 'folder-row';
    row.append(textInput(folder.from, t('folderFrom'), (v) => { folder.from = v; }));
    const arrow = document.createElement('span');
    arrow.className = 'arrow'; arrow.textContent = '→';
    row.append(arrow);
    row.append(textInput(folder.to, t('folderTo'), (v) => { folder.to = v; }));
    const del = document.createElement('button');
    del.className = 'link'; del.textContent = '✕'; del.title = t('remove');
    del.addEventListener('click', () => { acc.folders.splice(i, 1); render(); });
    row.append(del);
    box.append(row);
  });

  const hint = document.createElement('div');
  hint.className = 'hint'; hint.textContent = t('folderHint');
  const add = document.createElement('button');
  add.textContent = t('addFolder');
  add.style.marginTop = '.5rem';
  add.addEventListener('click', () => { acc.folders.push({ from: '', to: '' }); render(); });
  box.append(hint, add);
  return box;
}

function renderAccounts() {
  const container = $('accounts');
  container.textContent = '';
  state.accounts.forEach((acc, i) => {
    const card = document.createElement('div');
    card.className = 'card';

    const head = document.createElement('div');
    head.className = 'card-head';
    const h = document.createElement('h3');
    h.textContent = t('account') + ' ' + (i + 1);
    head.append(h);
    if (state.accounts.length > 1) {
      const del = document.createElement('button');
      del.className = 'link'; del.textContent = t('remove');
      del.addEventListener('click', () => { state.accounts.splice(i, 1); render(); });
      head.append(del);
    }
    card.append(head);

    const top = document.createElement('div');
    top.className = 'grid';
    top.append(field(t('name'), textInput(acc.name, 'trabajo', (v) => { acc.name = v; }), t('nameHint')));
    top.append(field(t('interval'), textInput(acc.interval, '5m', (v) => { acc.interval = v; }), t('intervalHint')));
    card.append(top);

    card.append(renderEndpoint(acc, 'source', t('source')));
    card.append(renderEndpoint(acc, 'dest', t('dest')));
    card.append(renderFolders(acc));
    container.append(card);
  });
}

function refreshOutput() {
  $('yaml-out').textContent = C.buildYAML(state, yamlText());
  $('secrets-out').textContent = C.buildSecrets(state, secretsText());

  const errors = errorMessages();
  const box = $('errors');
  const status = $('out-status');
  if (errors.length === 0) {
    box.hidden = true;
    status.textContent = '✓ ' + t('valid');
  } else {
    box.hidden = false;
    box.textContent = '';
    const title = document.createElement('div');
    title.textContent = t('errTitle');
    const list = document.createElement('ul');
    errors.forEach((e) => {
      const li = document.createElement('li');
      li.textContent = e;
      list.append(li);
    });
    box.append(title, list);
    status.textContent = '';
  }
}

function applyStaticStrings() {
  document.querySelectorAll('[data-i18n]').forEach((el) => {
    const key = el.dataset.i18n;
    const value = STRINGS[lang][key];
    if (value === undefined) return;
    if (/<[a-z]/i.test(value)) el.innerHTML = value;
    else el.textContent = value;
  });
  document.documentElement.lang = lang;
  $('lang-es').className = lang === 'es' ? 'primary' : '';
  $('lang-en').className = lang === 'en' ? 'primary' : '';
}

function render() {
  applyStaticStrings();
  renderAccounts();
  refreshOutput();
}

/* ---------- import ---------- */

function loadFromYAML(text) {
  const loaded = C.stateFromYAML(text);
  state.accounts = loaded.accounts;
  state.log = loaded.log;
  $('log-max').value = state.log.maxSize;
  $('log-keep').value = state.log.keep;
}

/* ---------- wiring ---------- */

function download(filename, content) {
  const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url; a.download = filename;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

async function copyToClipboard(text, button) {
  const original = button.textContent;
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = document.createElement('textarea');
    ta.value = text;
    document.body.append(ta);
    ta.select();
    document.execCommand('copy');
    ta.remove();
  }
  button.textContent = t('copied');
  setTimeout(() => { button.textContent = original; }, 1200);
}

$('add-account').addEventListener('click', () => {
  state.accounts.push(C.newAccount(''));
  render();
});
$('log-max').addEventListener('input', (e) => { state.log.maxSize = e.target.value; refreshOutput(); });
$('log-keep').addEventListener('input', (e) => { state.log.keep = e.target.value; refreshOutput(); });
$('copy-yaml').addEventListener('click', (e) => copyToClipboard(C.buildYAML(state, yamlText()), e.target));
$('copy-secrets').addEventListener('click', (e) => copyToClipboard(C.buildSecrets(state, secretsText()), e.target));
$('download-yaml').addEventListener('click', () => download('config.yaml', C.buildYAML(state, yamlText())));
$('download-secrets').addEventListener('click', () => download('secrets.env', C.buildSecrets(state, secretsText())));
$('lang-es').addEventListener('click', () => { lang = 'es'; render(); });
$('lang-en').addEventListener('click', () => { lang = 'en'; render(); });
$('import-btn').addEventListener('click', () => {
  const status = $('import-status');
  try {
    loadFromYAML($('import-text').value);
    render();
    status.textContent = t('importOk');
    status.style.color = 'var(--ok)';
  } catch (err) {
    status.textContent = t('importErr') + err.message;
    status.style.color = 'var(--danger)';
  }
});

render();
