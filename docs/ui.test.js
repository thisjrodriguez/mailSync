'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

// loadPage runs index.html with its real scripts, so a broken reference or a
// missing element fails the test instead of only showing up in a browser.
function loadPage() {
  const dom = new JSDOM(fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8'), {
    runScripts: 'dangerously',
    resources: undefined,
    url: 'https://example.invalid/',
  });
  const { window } = dom;
  const errors = [];
  window.addEventListener('error', (e) => errors.push(e.error || e.message));

  // JSDOM does not fetch external scripts, so inject them in order.
  for (const file of ['config.js', 'strings.js', 'app.js']) {
    const script = window.document.createElement('script');
    script.textContent = fs.readFileSync(path.join(__dirname, file), 'utf8');
    window.document.body.append(script);
  }
  assert.deepStrictEqual(errors, [], 'la página no debe lanzar errores al cargar');
  return window;
}

const $ = (w, id) => w.document.getElementById(id);
const type = (input, value) => {
  input.value = value;
  input.dispatchEvent(new input.ownerDocument.defaultView.Event('input', { bubbles: true }));
};
const click = (el) => el.dispatchEvent(new el.ownerDocument.defaultView.MouseEvent('click', { bubbles: true }));

test('la página carga y muestra una cuenta vacía', () => {
  const w = loadPage();
  assert.strictEqual(w.document.querySelectorAll('#accounts .card').length, 1);
  assert.ok($(w, 'yaml-out').textContent.startsWith('accounts:'));
});

// The whole point of the page: never ask for a password.
test('no existe ningún campo de contraseña', () => {
  const w = loadPage();
  assert.strictEqual(w.document.querySelectorAll('input[type=password]').length, 0);
  const html = w.document.body.innerHTML.toLowerCase();
  assert.ok(!html.includes('type="password"'));
});

test('rellenar el formulario produce un YAML válido', () => {
  const w = loadPage();
  const inputs = w.document.querySelectorAll('#accounts input[type=text]');
  // Orden: nombre, intervalo, [origen] host, usuario, variable, fingerprint,
  //        [destino] usuario, variable, fingerprint, [carpetas] from, to
  type(inputs[0], 'trabajo');
  const byPlaceholder = (p) =>
    [...w.document.querySelectorAll('#accounts input[type=text]')].find((i) => i.placeholder === p);
  type(byPlaceholder('mail.midominio.com'), 'mail.midominio.com');
  type(byPlaceholder('usuario@midominio.com'), 'usuario@midominio.com');
  type(byPlaceholder('cuenta@gmail.com'), 'cuenta@gmail.com');
  type(byPlaceholder('ORIGEN_PASS'), 'TRABAJO_PASS');
  type(byPlaceholder('GMAIL_PASS'), 'GMAIL_PASS');

  const yaml = $(w, 'yaml-out').textContent;
  assert.ok(yaml.includes('name: trabajo'));
  assert.ok(yaml.includes('host: mail.midominio.com'));
  assert.ok(yaml.includes('password: ${TRABAJO_PASS}'));
  assert.ok(yaml.includes('type: gmail'));
  assert.strictEqual($(w, 'errors').hidden, true, 'no debería haber errores');
  assert.ok($(w, 'out-status').textContent.includes('✓'));

  const secrets = $(w, 'secrets-out').textContent;
  assert.ok(secrets.includes('TRABAJO_PASS='));
  assert.ok(secrets.includes('GMAIL_PASS='));
});

test('los errores se muestran mientras la configuración esté incompleta', () => {
  const w = loadPage();
  assert.strictEqual($(w, 'errors').hidden, false, 'una cuenta vacía debe dar errores');
  assert.ok($(w, 'errors').textContent.length > 0);
});

test('añadir y quitar cuentas', () => {
  const w = loadPage();
  click($(w, 'add-account'));
  assert.strictEqual(w.document.querySelectorAll('#accounts .card').length, 2);
  const remove = w.document.querySelector('#accounts .card button.link');
  click(remove);
  assert.strictEqual(w.document.querySelectorAll('#accounts .card').length, 1);
});

test('añadir y quitar carpetas', () => {
  const w = loadPage();
  const before = w.document.querySelectorAll('.folder-row').length;
  const addFolder = [...w.document.querySelectorAll('#accounts button')]
    .find((b) => /carpeta|folder/i.test(b.textContent));
  click(addFolder);
  assert.strictEqual(w.document.querySelectorAll('.folder-row').length, before + 1);
});

test('importar un config.yaml rellena el formulario', () => {
  const w = loadPage();
  const yaml = [
    'accounts:',
    '  - name: importada',
    '    source:',
    '      host: mail.ejemplo.net',
    '      port: 143',
    '      user: yo@ejemplo.net',
    '      password: ${MI_CLAVE}',
    '      tls: starttls',
    '    dest:',
    '      type: gmail',
    '      user: yo@gmail.com',
    '      password: ${GM}',
    '    folders:',
    '      - INBOX',
    '      - from: Sent',
    '        to: ejemplo/Enviados',
    '    interval: 30s',
    '',
    'log:',
    '  max_size: 1MB',
    '  keep: 5',
  ].join('\n');
  $(w, 'import-text').value = yaml;
  click($(w, 'import-btn'));

  assert.ok($(w, 'import-status').textContent.length > 0);
  const out = $(w, 'yaml-out').textContent;
  assert.ok(out.includes('name: importada'), out);
  assert.ok(out.includes('port: 143'));
  assert.ok(out.includes('tls: starttls'));
  assert.ok(out.includes('to: ejemplo/Enviados'));
  assert.ok(out.includes('interval: 30s'));
  assert.ok(out.includes('max_size: 1MB'));
  assert.strictEqual($(w, 'log-keep').value, '5');
});

test('un YAML ilegible avisa sin romper la página', () => {
  const w = loadPage();
  $(w, 'import-text').value = 'esto no es yaml: [[[';
  click($(w, 'import-btn'));
  assert.ok($(w, 'import-status').textContent.length > 0);
  assert.strictEqual(w.document.querySelectorAll('#accounts .card').length, 1, 'el formulario sigue en pie');
});

test('el cambio de idioma traduce la interfaz', () => {
  const w = loadPage();
  click($(w, 'lang-en'));
  assert.strictEqual(w.document.documentElement.lang, 'en');
  assert.ok(w.document.body.textContent.includes('Add account'));
  click($(w, 'lang-es'));
  assert.strictEqual(w.document.documentElement.lang, 'es');
  assert.ok(w.document.body.textContent.includes('Añadir cuenta'));
});

test('cambiar el destino a servidor propio pide host', () => {
  const w = loadPage();
  const selects = [...w.document.querySelectorAll('#accounts select')];
  const destPreset = selects.find((s) => s.value === 'gmail');
  destPreset.value = 'custom';
  destPreset.dispatchEvent(new w.Event('change', { bubbles: true }));
  const yaml = $(w, 'yaml-out').textContent;
  assert.ok(!yaml.includes('type: gmail'));
});

// A missing translation key silently renders the key itself, which is easy to
// ship without noticing. This catches it.
test('no queda ninguna clave de traducción sin definir', () => {
  const fsMod = require('node:fs');
  const source = fsMod.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

  const used = new Set();
  // Only literal lookups: t('key') or t('key', arg). A dynamic t('err_' + code)
  // is covered separately below, by checking the codes config.js can emit.
  for (const m of source.matchAll(/\bt\('([A-Za-z_]\w*)'\s*[,)]/g)) used.add(m[1]);
  for (const m of fsMod.readFileSync(path.join(__dirname, 'index.html'), 'utf8')
    .matchAll(/data-i18n="([^"]+)"/g)) used.add(m[1]);

  const strings = require('./strings.js');

  for (const lang of ['es', 'en']) {
    const missing = [...used].filter((k) => strings[lang][k] === undefined);
    assert.deepStrictEqual(missing, [], 'faltan claves en "' + lang + '": ' + missing.join(', '));
  }
  // Every validation code config.js can emit needs a matching message.
  const configSource = fsMod.readFileSync(path.join(__dirname, 'config.js'), 'utf8');
  const codes = [...configSource.matchAll(/add\('(\w+)'/g)].map((m) => 'err_' + m[1]);
  assert.ok(codes.length >= 10, 'se esperaban varios códigos de error, hay ' + codes.length);
  for (const lang of ['es', 'en']) {
    const missing = codes.filter((k) => strings[lang][k] === undefined);
    assert.deepStrictEqual(missing, [], 'faltan mensajes de error en "' + lang + '": ' + missing.join(', '));
  }

  // Both languages must cover the same keys.
  const es = Object.keys(strings.es).sort();
  const en = Object.keys(strings.en).sort();
  assert.deepStrictEqual(es, en, 'los dos idiomas deben tener las mismas claves');
});
