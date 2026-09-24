'use strict';

const test = require('node:test');
const assert = require('node:assert');
const c = require('./config.js');

const TEXT = { promptComment: 'sin contraseña aquí', noVars: 'sin variables', secretsHeader: '# 0600' };

function fullState() {
  const s = c.newState('trabajo');
  const a = s.accounts[0];
  a.source.host = 'mail.midominio.com';
  a.source.user = 'usuario@midominio.com';
  a.source.pwVar = 'TRABAJO_PASS';
  a.dest.user = 'cuenta@gmail.com';
  a.dest.pwVar = 'GMAIL_PASS';
  return s;
}

test('el YAML por defecto omite lo que ya es el valor por defecto', () => {
  const yaml = c.buildYAML(fullState(), TEXT);
  assert.ok(!yaml.includes('port:'), 'el puerto 993 no debería aparecer');
  assert.ok(!yaml.includes('tls:'), 'tls por defecto no debería aparecer');
  assert.ok(!yaml.includes('interval:'), 'el intervalo por defecto no debería aparecer');
  assert.ok(!yaml.includes('log:'), 'la sección log por defecto no debería aparecer');
});

test('type: gmail sustituye a host y puerto', () => {
  const yaml = c.buildYAML(fullState(), TEXT);
  const dest = yaml.slice(yaml.indexOf('dest:'));
  assert.ok(dest.includes('type: gmail'));
  assert.ok(!dest.includes('host:'), 'gmail no debe llevar host');
});

test('una carpeta renombrada se emite como from/to', () => {
  const s = fullState();
  s.accounts[0].folders = [{ from: 'INBOX', to: 'INBOX' }, { from: 'INBOX.Sent', to: 'midominio/Enviados' }];
  const yaml = c.buildYAML(s, TEXT);
  assert.ok(yaml.includes('      - INBOX\n'), 'la carpeta sin renombrar va como escalar');
  assert.ok(yaml.includes('      - from: INBOX.Sent\n        to: midominio/Enviados\n'));
});

test('el modo "preguntar" no escribe ninguna contraseña', () => {
  const s = fullState();
  s.accounts[0].source.pwMode = 'prompt';
  const yaml = c.buildYAML(s, TEXT);
  const source = yaml.slice(yaml.indexOf('source:'), yaml.indexOf('dest:'));
  assert.ok(!source.includes('password:'), 'no debe haber línea de contraseña');
  assert.ok(source.includes('# sin contraseña aquí'));
});

test('el ciclo generar -> importar -> generar es estable', () => {
  const s = fullState();
  s.accounts[0].source.port = 143;
  s.accounts[0].source.tls = 'starttls';
  s.accounts[0].source.fingerprint = 'AB:' + 'cd'.repeat(31) + ':EF';
  s.accounts[0].folders = [{ from: 'INBOX', to: 'INBOX' }, { from: 'Sent', to: 'midominio/Enviados' }];
  s.accounts[0].interval = '90s';
  s.log = { maxSize: '2MB', keep: 0 };
  s.accounts.push(c.newAccount('personal'));
  Object.assign(s.accounts[1].source, { host: 'imap.otro.net', user: 'yo@otro.net', pwVar: 'OTRO' });
  Object.assign(s.accounts[1].dest, { user: 'yo@gmail.com', pwVar: 'GM' });

  const first = c.buildYAML(s, TEXT);
  const second = c.buildYAML(c.stateFromYAML(first), TEXT);
  assert.strictEqual(second, first, 'el YAML debería sobrevivir a un viaje de ida y vuelta');
});

test('importar recupera los campos uno a uno', () => {
  const s = fullState();
  s.accounts[0].source.port = 143;
  s.accounts[0].source.tls = 'starttls';
  const back = c.stateFromYAML(c.buildYAML(s, TEXT));
  const ep = back.accounts[0].source;
  assert.strictEqual(back.accounts[0].name, 'trabajo');
  assert.strictEqual(ep.host, 'mail.midominio.com');
  assert.strictEqual(ep.port, 143);
  assert.strictEqual(ep.tls, 'starttls');
  assert.strictEqual(ep.pwVar, 'TRABAJO_PASS');
  assert.strictEqual(back.accounts[0].dest.preset, 'gmail');
});

// Importing your own file should give it back unchanged, literal password
// included -- otherwise a round trip would silently drop it.
test('una contraseña literal importada se conserva en su modo', () => {
  const yaml = [
    'accounts:',
    '  - name: x',
    '    source:',
    '      host: h',
    '      user: u',
    '      password: secreto-de-verdad',
    '    dest:',
    '      type: gmail',
    '      user: u2',
    '      password: ${GM}',
    '    folders:',
    '      - INBOX',
  ].join('\n');
  const state = c.stateFromYAML(yaml);
  assert.strictEqual(state.accounts[0].source.pwMode, 'literal');
  assert.strictEqual(state.accounts[0].source.pwValue, 'secreto-de-verdad');
  assert.strictEqual(state.accounts[0].dest.pwMode, 'var');
  assert.strictEqual(state.accounts[0].dest.pwVar, 'GM');
  assert.ok(c.hasPlainPasswords(state));
  // It must never leak into the secrets template, which is meant to be shared.
  assert.ok(!c.buildSecrets(state, {}).includes('secreto-de-verdad'));
});

test('el entrecomillado solo se aplica cuando hace falta', () => {
  ['usuario@midominio.com', 'mail.midominio.com', 'INBOX.Sent', 'midominio/Enviados', '5m', 'Carpeta con espacios']
    .forEach((v) => assert.strictEqual(c.scalar(v), v, v + ' no necesita comillas'));
  ['', 'yes', '993', 'clave: valor', '@arroba', 'con # almohadilla']
    .forEach((v) => assert.ok(c.scalar(v).startsWith('"'), JSON.stringify(v) + ' sí necesita comillas'));
});

test('los valores entrecomillados sobreviven al viaje de ida y vuelta', () => {
  const s = fullState();
  s.accounts[0].folders = [{ from: 'Carpeta: rara', to: 'Carpeta: rara' }];
  const back = c.stateFromYAML(c.buildYAML(s, TEXT));
  assert.strictEqual(back.accounts[0].folders[0].from, 'Carpeta: rara');
});

test('la validación detecta cada fallo', () => {
  const codes = (state) => c.validate(state).map((e) => e.code);

  const empty = fullState();
  empty.accounts[0].name = '';
  assert.ok(codes(empty).includes('name'));

  const dup = fullState();
  dup.accounts.push(c.newAccount('trabajo'));
  assert.ok(codes(dup).includes('dupName'));

  const noHost = fullState();
  noHost.accounts[0].source.host = '';
  assert.ok(codes(noHost).includes('host'));

  const badPort = fullState();
  badPort.accounts[0].source.port = 99999;
  assert.ok(codes(badPort).includes('port'));

  const badVar = fullState();
  badVar.accounts[0].source.pwVar = '2MAL';
  assert.ok(codes(badVar).includes('varName'));

  const noVar = fullState();
  noVar.accounts[0].source.pwVar = '';
  assert.ok(codes(noVar).includes('varMissing'));

  const badFp = fullState();
  badFp.accounts[0].source.fingerprint = 'abc';
  assert.ok(codes(badFp).includes('fingerprint'));

  const badInterval = fullState();
  badInterval.accounts[0].interval = '5 minutos';
  assert.ok(codes(badInterval).includes('interval'));

  const noFolders = fullState();
  noFolders.accounts[0].folders = [];
  assert.ok(codes(noFolders).includes('folders'));

  const badLog = fullState();
  badLog.log = { maxSize: 'mucho', keep: -1 };
  assert.ok(codes(badLog).includes('logSize'));
  assert.ok(codes(badLog).includes('logKeep'));

  assert.deepStrictEqual(codes(fullState()), [], 'una configuración correcta no debe dar errores');
});

test('secrets.env lista cada variable una sola vez', () => {
  const s = fullState();
  s.accounts.push(c.newAccount('otra'));
  Object.assign(s.accounts[1].source, { host: 'h', user: 'u', pwVar: 'TRABAJO_PASS' });
  Object.assign(s.accounts[1].dest, { user: 'u2', pwVar: 'NUEVA' });
  const out = c.buildSecrets(s, TEXT);
  assert.strictEqual(out.match(/^TRABAJO_PASS=$/gm).length, 1);
  assert.ok(out.includes('NUEVA='));
  assert.ok(!out.includes('secreto'), 'nunca debe contener valores');
});

test('el parser rechaza lo que no entiende', () => {
  // Texto suelto: falla al parsear, señalando la línea.
  assert.throws(() => c.stateFromYAML('esto no es una configuración'), /line 1/);
  // YAML válido pero que no es una configuración de mailsync.
  assert.throws(() => c.stateFromYAML('otra_cosa:\n  clave: valor\n'), /no accounts/);
  // Tabuladores: YAML no los admite para indentar y el error debe decirlo.
  assert.throws(() => c.parseYAML('accounts:\n\t- name: x'), /tabs/);
});
