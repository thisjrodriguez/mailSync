'use strict';

/* Pure configuration model for the mailsync generator: build YAML, parse it
   back, validate it. No DOM and no translated text -- validation returns
   codes that the interface turns into sentences. Loadable both as a browser
   global and as a Node module, so it can be tested outside a browser. */

(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.MailsyncConfig = api;
}(typeof self !== 'undefined' ? self : this, function () {

  const DEFAULT_INTERVAL = '5m';
  const DEFAULT_PORT = 993;
  const DEFAULT_LOG = { maxSize: '5MB', keep: 3 };

  const GO_DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;
  const SIZE = /^\d+(\.\d+)?\s*(GB|MB|KB|G|M|K|B)?$/i;
  const PLACEHOLDER = /^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$/;

  function newEndpoint(preset) {
    return {
      preset: preset || 'custom',
      host: '', port: DEFAULT_PORT, user: '', tls: 'tls',
      fingerprint: '', password: '',
    };
  }

  function newAccount(name) {
    return {
      name: name || '',
      source: newEndpoint('custom'),
      dest: newEndpoint('gmail'),
      folders: [{ from: 'INBOX', to: 'INBOX' }],
      interval: DEFAULT_INTERVAL,
    };
  }

  function newState(name) {
    return { accounts: [newAccount(name)], log: Object.assign({}, DEFAULT_LOG) };
  }

  /* ---------- emitting ---------- */

  // needsQuotes follows what YAML actually reserves rather than quoting on
  // sight: most indicators only matter at the start of a scalar, and a colon
  // only ends a key when a space follows it. That keeps e-mail addresses and
  // hostnames unquoted, which is what people expect to read.
  function needsQuotes(value) {
    if (value === '') return true;
    if (/^\s|\s$/.test(value)) return true;
    if (/^[-?:,\[\]{}#&*!|>'"%@`]/.test(value)) return true;
    if (/:\s/.test(value) || /:$/.test(value)) return true;
    if (/\s#/.test(value)) return true;
    if (/^(true|false|yes|no|on|off|null|~)$/i.test(value)) return true;
    if (/^-?\d+(\.\d+)?$/.test(value)) return true;
    return false;
  }

  function scalar(value) {
    const s = String(value);
    return needsQuotes(s) ? '"' + s.replace(/\\/g, '\\\\').replace(/"/g, '\\"') + '"' : s;
  }

  function endpointYAML(ep, indent, promptComment) {
    let out = '';
    if (ep.preset === 'gmail') {
      out += indent + 'type: gmail\n';
    } else {
      out += indent + 'host: ' + scalar(ep.host) + '\n';
      if (Number(ep.port) !== DEFAULT_PORT) out += indent + 'port: ' + Number(ep.port) + '\n';
    }
    out += indent + 'user: ' + scalar(ep.user) + '\n';
    // The field holds whatever goes after "password:" -- a real password or a
    // ${VAR} placeholder. Left empty, the line is omitted and mailsync asks
    // for it at startup.
    if (ep.password) out += indent + 'password: ' + scalar(ep.password) + '\n';
    if (ep.preset !== 'gmail' && ep.tls !== 'tls') out += indent + 'tls: ' + ep.tls + '\n';
    if (ep.fingerprint) out += indent + 'fingerprint: ' + ep.fingerprint.replace(/[\s:-]/g, '').toLowerCase() + '\n';
    return out;
  }

  function buildYAML(state, text) {
    const promptComment = (text && text.promptComment) || 'password asked at startup';
    let out = 'accounts:\n';
    state.accounts.forEach((acc, i) => {
      if (i > 0) out += '\n';
      out += '  - name: ' + scalar(acc.name) + '\n\n';
      out += '    source:\n' + endpointYAML(acc.source, '      ', promptComment) + '\n';
      out += '    dest:\n' + endpointYAML(acc.dest, '      ', promptComment) + '\n';
      out += '    folders:\n';
      acc.folders.forEach((f) => {
        if (f.to && f.to !== f.from) {
          out += '      - from: ' + scalar(f.from) + '\n';
          out += '        to: ' + scalar(f.to) + '\n';
        } else {
          out += '      - ' + scalar(f.from) + '\n';
        }
      });
      if (acc.interval && acc.interval !== DEFAULT_INTERVAL) {
        out += '\n    interval: ' + acc.interval + '\n';
      }
    });
    if (state.log.maxSize !== DEFAULT_LOG.maxSize || Number(state.log.keep) !== DEFAULT_LOG.keep) {
      out += '\nlog:\n';
      out += '  max_size: ' + state.log.maxSize + '\n';
      out += '  keep: ' + Number(state.log.keep) + '\n';
    }
    return out;
  }

  // variableNames picks up any ${VAR} used as a password, so the secrets
  // template lists exactly what needs filling in.
  function variableNames(state) {
    const names = [];
    state.accounts.forEach((acc) => {
      [acc.source, acc.dest].forEach((ep) => {
        const match = PLACEHOLDER.exec(ep.password || '');
        if (match && !names.includes(match[1])) names.push(match[1]);
      });
    });
    return names;
  }

  function buildSecrets(state, text) {
    const names = variableNames(state);
    if (names.length === 0) return '# ' + ((text && text.noVars) || 'no variables used') + '\n';
    const header = (text && text.secretsHeader) || '# save with 0600 permissions';
    return header + '\n\n' + names.map((n) => n + '=').join('\n') + '\n';
  }

  /* ---------- validation, mirroring what the Go side enforces ---------- */

  function validate(state) {
    const errors = [];
    const add = (code, ...args) => errors.push({ code, args });

    if (state.accounts.length === 0) add('noAccounts');

    const seen = new Set();
    state.accounts.forEach((acc, i) => {
      const label = acc.name.trim() || '#' + (i + 1);
      if (!acc.name.trim()) add('name', i + 1);
      else if (seen.has(acc.name)) add('dupName', acc.name);
      seen.add(acc.name);

      ['source', 'dest'].forEach((side) => {
        const ep = acc[side];
        if (ep.preset !== 'gmail' && !ep.host.trim()) add('host', label, side);
        if (!ep.user.trim()) add('user', label, side);
        const port = Number(ep.port);
        if (ep.preset !== 'gmail' && (!Number.isInteger(port) || port < 1 || port > 65535)) {
          add('port', label, side);
        }
        if (!ep.password) add('passwordMissing', label, side);
        if (ep.fingerprint && !/^[0-9a-f]{64}$/i.test(ep.fingerprint.replace(/[\s:-]/g, ''))) {
          add('fingerprint', label, side);
        }
      });

      if (acc.folders.length === 0) add('folders', label);
      if (acc.folders.some((f) => !f.from.trim())) add('folderFrom', label);
      if (acc.interval && !GO_DURATION.test(acc.interval.trim())) add('interval', label);
    });

    if (!SIZE.test(String(state.log.maxSize).trim())) add('logSize');
    if (!Number.isInteger(Number(state.log.keep)) || Number(state.log.keep) < 0) add('logKeep');
    return errors;
  }

  /* ---------- parsing back ---------- */

  // stripComment drops an unquoted trailing comment.
  function stripComment(line) {
    let quote = null;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (quote) { if (c === quote) quote = null; }
      else if (c === '"' || c === "'") quote = c;
      else if (c === '#' && (i === 0 || /\s/.test(line[i - 1]))) return line.slice(0, i);
    }
    return line;
  }

  function unquote(value) {
    const v = value.trim();
    if (v.length > 1 && ((v[0] === '"' && v.endsWith('"')) || (v[0] === "'" && v.endsWith("'")))) {
      return v.slice(1, -1).replace(/\\"/g, '"').replace(/\\\\/g, '\\');
    }
    return v;
  }

  // parseYAML handles the subset this generator emits: nested maps, sequences
  // of scalars and sequences of maps. It is not a general YAML parser, and it
  // says so by throwing on anything it does not recognise.
  function parseYAML(text) {
    const lines = [];
    text.split(/\r?\n/).forEach((raw, i) => {
      const line = stripComment(raw).replace(/\s+$/, '');
      if (!line.trim()) return;
      if (/^\t/.test(raw)) throw new Error('line ' + (i + 1) + ': YAML does not allow tabs for indentation');
      lines.push({ indent: line.match(/^ */)[0].length, text: line.trim(), n: i + 1 });
    });
    if (lines.length === 0) return {};

    let pos = 0;
    const isSeqItem = (l) => l.text === '-' || l.text.startsWith('- ');

    function parseBlock(indent) {
      if (pos >= lines.length) return null;
      return isSeqItem(lines[pos]) ? parseSeq(indent) : parseMap(indent);
    }

    function parseMap(indent) {
      const obj = {};
      while (pos < lines.length && lines[pos].indent === indent && !isSeqItem(lines[pos])) {
        const cur = lines[pos];
        const m = cur.text.match(/^([^:]+):\s*(.*)$/);
        if (!m) throw new Error('line ' + cur.n + ': expected "key: value", got "' + cur.text + '"');
        const key = m[1].trim();
        const rest = m[2].trim();
        pos++;
        if (rest === '') {
          if (pos < lines.length && lines[pos].indent > indent) obj[key] = parseBlock(lines[pos].indent);
          else obj[key] = null;
        } else {
          obj[key] = unquote(rest);
        }
      }
      return obj;
    }

    function parseSeq(indent) {
      const arr = [];
      while (pos < lines.length && lines[pos].indent === indent && isSeqItem(lines[pos])) {
        const cur = lines[pos];
        const rest = cur.text === '-' ? '' : cur.text.slice(2).trim();
        if (rest === '') {
          pos++;
          if (pos < lines.length && lines[pos].indent > indent) arr.push(parseBlock(lines[pos].indent));
          else arr.push(null);
        } else if (!/^["']/.test(rest) && /^[^:]+:/.test(rest)) {
          // "- key: value" opens a map whose remaining keys sit two deeper.
          const itemIndent = indent + 2;
          lines[pos] = { indent: itemIndent, text: rest, n: cur.n };
          arr.push(parseMap(itemIndent));
        } else {
          arr.push(unquote(rest));
          pos++;
        }
      }
      return arr;
    }

    return parseBlock(lines[0].indent) || {};
  }

  function endpointFromYAML(raw) {
    const ep = newEndpoint('custom');
    if (!raw) return ep;
    if (raw.type && String(raw.type).toLowerCase() === 'gmail') ep.preset = 'gmail';
    ep.host = raw.host || '';
    ep.port = raw.port ? Number(raw.port) : DEFAULT_PORT;
    ep.user = raw.user || '';
    ep.tls = raw.tls || (Number(ep.port) === 143 ? 'starttls' : 'tls');
    ep.fingerprint = raw.fingerprint || '';
    ep.password = raw.password || '';
    return ep;
  }

  function stateFromYAML(text) {
    const doc = parseYAML(text);
    if (!doc || !Array.isArray(doc.accounts) || doc.accounts.length === 0) {
      throw new Error('no accounts found');
    }
    const state = {
      accounts: doc.accounts.map((raw) => ({
        name: raw.name || '',
        source: endpointFromYAML(raw.source),
        dest: endpointFromYAML(raw.dest),
        folders: (raw.folders || []).map((f) => (
          typeof f === 'string'
            ? { from: f, to: f }
            : { from: (f && f.from) || '', to: (f && (f.to || f.from)) || '' }
        )),
        interval: raw.interval || DEFAULT_INTERVAL,
      })),
      log: Object.assign({}, DEFAULT_LOG),
    };
    if (doc.log) {
      if (doc.log.max_size) state.log.maxSize = doc.log.max_size;
      if (doc.log.keep !== null && doc.log.keep !== undefined) state.log.keep = Number(doc.log.keep);
    }
    return state;
  }

  return {
    DEFAULT_INTERVAL, DEFAULT_PORT, DEFAULT_LOG,
    newEndpoint, newAccount, newState,
    needsQuotes, scalar, buildYAML, buildSecrets, variableNames,
    validate, parseYAML, stateFromYAML,
  };
}));
