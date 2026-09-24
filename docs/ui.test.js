'use strict';

const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { JSDOM } = require('jsdom');

// loadPage runs index.html with its real scripts, so a broken reference or a
// renamed element fails here rather than only in a browser.
function loadPage(options) {
  // jsdom reports en-US; pin Spanish unless a test is about detection itself.
  const opts = Object.assign({ languages: ['es-ES'] }, options || {});
  const dom = new JSDOM(fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8'), {
    runScripts: 'dangerously',
    url: 'https://example.invalid/',
  });
  const { window } = dom;

  {
    Object.defineProperty(window.navigator, 'languages', { value: opts.languages, configurable: true });
    Object.defineProperty(window.navigator, 'language', { value: opts.languages[0], configurable: true });
  }
  if (opts.stored) window.localStorage.setItem('mailsync.lang', opts.stored);

  // jsdom has no object URLs; downloads only need to not explode.
  window.URL.createObjectURL = () => 'blob:stub';
  window.URL.revokeObjectURL = () => {};

  const errors = [];
  window.addEventListener('error', (e) => errors.push(e.error || e.message));
  // Load exactly the files index.html asks for, so a renamed or forgotten
  // script is caught here too.
  const scriptSources = [...fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8')
    .matchAll(/<script src="([^"?]+)/g)].map((m) => m[1]);
  assert.deepStrictEqual(scriptSources, ['config.js', 'strings.js', 'app.js']);
  for (const file of scriptSources) {
    const script = window.document.createElement('script');
    script.textContent = fs.readFileSync(path.join(__dirname, file), 'utf8');
    window.document.body.append(script);
  }
  assert.deepStrictEqual(errors, [], 'la página no debe lanzar errores al cargar');
  return window;
}

const $ = (w, id) => w.document.getElementById(id);
const sideItems = (w) => [...w.document.querySelectorAll('#sidebar .side-item')];
const mainText = (w) => $(w, 'main-inner').textContent;
const output = (w) => { const pre = w.document.querySelector('#main-inner pre'); return pre ? pre.textContent : ''; };

const click = (el) => el.dispatchEvent(new el.ownerDocument.defaultView.MouseEvent('click', { bubbles: true }));
const type = (input, value) => {
  input.value = value;
  input.dispatchEvent(new input.ownerDocument.defaultView.Event('input', { bubbles: true }));
};
const goTo = (w, label) => {
  const item = sideItems(w).find((i) => i.textContent.includes(label));
  assert.ok(item, 'no hay ningún elemento de menú con "' + label + '"');
  click(item);
};
const byPlaceholder = (w, p) =>
  [...w.document.querySelectorAll('#main-inner input')].find((i) => i.placeholder === p);

function fillAccount(w) {
  type(byPlaceholder(w, 'trabajo'), 'trabajo');
  type(byPlaceholder(w, 'mail.midominio.com'), 'mail.midominio.com');
  type(byPlaceholder(w, 'usuario@midominio.com'), 'usuario@midominio.com');
  type(byPlaceholder(w, 'ORIGEN_PASS'), 'TRABAJO_PASS');
  type(byPlaceholder(w, 'cuenta@gmail.com'), 'cuenta@gmail.com');
  type(byPlaceholder(w, 'GMAIL_PASS'), 'GMAIL_PASS');
}

test('arranca en la primera cuenta y la lista en la barra lateral', () => {
  const w = loadPage();
  assert.ok(sideItems(w).length >= 4, 'la barra lateral debe tener varias entradas');
  assert.strictEqual(sideItems(w)[0].getAttribute('aria-current'), 'true');
  assert.ok(mainText(w).includes('ORIGEN') || mainText(w).includes('SOURCE') ||
            mainText(w).toLowerCase().includes('origen'));
});

// By default the page never asks for a password. Typing one is opt-in.
test('no hay ningún campo de contraseña salvo que se pida', () => {
  const w = loadPage();
  assert.strictEqual(w.document.querySelectorAll('input[type=password]').length, 0);
  ['config.yaml', 'secrets.env'].forEach((label) => {
    goTo(w, label);
    assert.strictEqual(w.document.querySelectorAll('input[type=password]').length, 0);
  });
});

test('el modo "escribirla aquí" es opcional y mantiene el campo a salvo del gestor de contraseñas', () => {
  const w = loadPage();
  const literal = [...w.document.querySelectorAll('#main-inner input[type=radio]')]
    .find((r) => r.parentElement.textContent.includes('Escribirla'));
  assert.ok(literal, 'debería existir la opción de escribir la contraseña');
  click(literal);

  const input = w.document.querySelector('#main-inner input[type=password]');
  assert.ok(input, 'ahora sí debe haber un campo');
  assert.strictEqual(input.getAttribute('autocomplete'), 'off', 'no debe ofrecerse al autocompletado');
  assert.strictEqual(input.getAttribute('data-lpignore'), 'true');

  type(input, 'clave-real');
  goTo(w, 'config.yaml');
  assert.ok(output(w).includes('password: clave-real'));
  // The warning has to appear exactly when the file carries a secret.
  assert.ok(/chmod 600/.test(mainText(w)), 'debe avisar de proteger el fichero');

  goTo(w, 'secrets.env');
  assert.ok(!output(w).includes('clave-real'), 'la contraseña escrita no va en secrets.env');
});

test('una contraseña escrita a medias se marca como error', () => {
  const w = loadPage();
  fillAccount(w);
  const literal = [...w.document.querySelectorAll('#main-inner input[type=radio]')]
    .find((r) => r.parentElement.textContent.includes('Escribirla'));
  click(literal);
  assert.ok($(w, 'status-chip').className.includes('bad'), 'sin valor debe contar como error');
});

test('sin contraseñas en claro no aparece el aviso', () => {
  const w = loadPage();
  fillAccount(w);
  goTo(w, 'config.yaml');
  assert.ok(!/chmod 600/.test(mainText(w)), 'no debe avisar si no hay secretos en el fichero');
});

test('rellenar el formulario produce un YAML válido y marca la configuración como correcta', () => {
  const w = loadPage();
  fillAccount(w);

  const chip = $(w, 'status-chip');
  assert.ok(chip.className.includes('ok'), 'el indicador debería estar en verde: ' + chip.textContent);

  goTo(w, 'config.yaml');
  const yaml = output(w);
  assert.ok(yaml.includes('name: trabajo'), yaml);
  assert.ok(yaml.includes('host: mail.midominio.com'));
  assert.ok(yaml.includes('password: ${TRABAJO_PASS}'));
  assert.ok(yaml.includes('type: gmail'));

  goTo(w, 'secrets.env');
  const secrets = output(w);
  assert.ok(secrets.includes('TRABAJO_PASS='));
  assert.ok(secrets.includes('GMAIL_PASS='));
  assert.ok(!secrets.includes('TRABAJO_PASS=a'), 'la plantilla no debe llevar valores');
});

test('el indicador cuenta los errores mientras falten datos', () => {
  const w = loadPage();
  const chip = $(w, 'status-chip');
  assert.ok(chip.className.includes('bad'));
  assert.ok(/\d+|1/.test(chip.textContent), chip.textContent);
});

test('la cuenta con errores queda marcada en la barra lateral', () => {
  const w = loadPage();
  assert.ok(sideItems(w)[0].querySelector('.dot'), 'debería haber un punto de aviso');
  fillAccount(w);
  assert.ok(!sideItems(w)[0].querySelector('.dot'), 'el aviso debe desaparecer al completarla');
});

test('añadir y eliminar cuentas desde la barra lateral', () => {
  const w = loadPage();
  const before = sideItems(w).length;
  goTo(w, '+');
  assert.strictEqual(sideItems(w).length, before + 1);

  const remove = [...w.document.querySelectorAll('#main-inner button')]
    .find((b) => /eliminar|delete/i.test(b.textContent));
  assert.ok(remove, 'debería poder eliminarse una cuenta cuando hay más de una');
  click(remove);
  assert.strictEqual(sideItems(w).length, before);
});

test('escribir el nombre actualiza la barra lateral sin perder el foco', () => {
  const w = loadPage();
  const input = byPlaceholder(w, 'trabajo');
  input.focus();
  type(input, 'personal');
  assert.ok(sideItems(w)[0].textContent.includes('personal'));
  assert.strictEqual(w.document.activeElement, input, 'el cursor debe seguir en el campo');
});

test('añadir y quitar carpetas', () => {
  const w = loadPage();
  const before = w.document.querySelectorAll('.folder-row').length;
  const add = [...w.document.querySelectorAll('#main-inner button')]
    .find((b) => /carpeta|folder/i.test(b.textContent));
  click(add);
  assert.strictEqual(w.document.querySelectorAll('.folder-row').length, before + 1);

  click(w.document.querySelector('.folder-row button.icon'));
  assert.strictEqual(w.document.querySelectorAll('.folder-row').length, before);
});

test('importar un config.yaml rellena el formulario', () => {
  const w = loadPage();
  goTo(w, 'Cargar');
  const area = w.document.querySelector('#main-inner textarea');
  area.value = [
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
  click([...w.document.querySelectorAll('#main-inner button')].find((b) => /cargar en|load into/i.test(b.textContent)));

  assert.ok(sideItems(w)[0].textContent.includes('importada'));
  goTo(w, 'config.yaml');
  const yaml = output(w);
  assert.ok(yaml.includes('port: 143'));
  assert.ok(yaml.includes('tls: starttls'));
  assert.ok(yaml.includes('to: ejemplo/Enviados'));
  assert.ok(yaml.includes('interval: 30s'));
  assert.ok(yaml.includes('max_size: 1MB'));
});

test('un YAML ilegible avisa y deja la página en pie', () => {
  const w = loadPage();
  goTo(w, 'Cargar');
  const area = w.document.querySelector('#main-inner textarea');
  area.value = 'esto no es yaml: [[[';
  click([...w.document.querySelectorAll('#main-inner button')].find((b) => /cargar en|load into/i.test(b.textContent)));
  assert.ok(mainText(w).length > 0);
  assert.ok(sideItems(w).length >= 4, 'la barra lateral sigue en pie');
});

test('el idioma se detecta solo a partir del navegador', () => {
  const english = loadPage({ languages: ['en-GB', 'es'] });
  assert.strictEqual(english.document.documentElement.lang, 'en');
  assert.strictEqual($(english, 'lang-select').value, 'en', 'el desplegable debe reflejar lo detectado');
  assert.ok(english.document.body.textContent.includes('Add account'));

  const spanish = loadPage({ languages: ['es-ES'] });
  assert.strictEqual(spanish.document.documentElement.lang, 'es');
  assert.strictEqual($(spanish, 'lang-select').value, 'es');
  assert.ok(spanish.document.body.textContent.includes('Añadir cuenta'));
});

test('el desplegable ofrece los dos idiomas con su bandera', () => {
  const w = loadPage();
  const options = [...$(w, 'lang-select').options];
  assert.deepStrictEqual(options.map((o) => o.value), ['es', 'en']);
  assert.ok(options[0].textContent.includes('\u{1F1EA}\u{1F1F8}'), 'falta la bandera de España');
  assert.ok(options[1].textContent.includes('\u{1F1EC}\u{1F1E7}'), 'falta la bandera del Reino Unido');
  assert.ok(options[0].textContent.includes('Español'));
  assert.ok(options[1].textContent.includes('English'));
});

test('elegir idioma en el desplegable manda sobre la detección y se recuerda', () => {
  const w = loadPage({ languages: ['es-ES'] });
  const selector = $(w, 'lang-select');
  selector.value = 'en';
  selector.dispatchEvent(new w.Event('change', { bubbles: true }));

  assert.strictEqual(w.document.documentElement.lang, 'en');
  assert.ok(w.document.body.textContent.includes('Add account'));
  assert.strictEqual(w.localStorage.getItem('mailsync.lang'), 'en');

  // A fresh visit keeps the choice even though the browser still says Spanish.
  const again = loadPage({ languages: ['es-ES'], stored: 'en' });
  assert.strictEqual(again.document.documentElement.lang, 'en');
  assert.strictEqual($(again, 'lang-select').value, 'en');
});

test('descargar no rompe nada', () => {
  const w = loadPage();
  fillAccount(w);
  const exportItem = sideItems(w).find((i) => /descargar|download all/i.test(i.textContent));
  assert.ok(exportItem);
  click(exportItem);
});

// A missing translation renders the key name to the user, which is easy to
// ship unnoticed. This catches it.
test('no queda ninguna clave de traducción sin definir', () => {
  const read = (f) => fs.readFileSync(path.join(__dirname, f), 'utf8');
  const used = new Set();
  for (const m of read('app.js').matchAll(/\bt\('([A-Za-z_]\w*)'\s*[,)]/g)) used.add(m[1]);
  for (const m of read('index.html').matchAll(/data-i18n="([^"]+)"/g)) used.add(m[1]);
  for (const m of read('config.js').matchAll(/add\('(\w+)'/g)) used.add('err_' + m[1]);

  const strings = require('./strings.js');
  assert.ok(used.size > 20, 'se esperaban bastantes claves, hay ' + used.size);
  for (const lang of ['es', 'en']) {
    const missing = [...used].filter((k) => strings[lang][k] === undefined);
    assert.deepStrictEqual(missing, [], 'faltan claves en "' + lang + '": ' + missing.join(', '));
  }
  assert.deepStrictEqual(Object.keys(strings.es).sort(), Object.keys(strings.en).sort());
});
