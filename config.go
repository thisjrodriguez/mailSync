package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultPort     = 993
	defaultInterval = 5 * time.Minute
	defaultLogSize  = 5 << 20 // 5 MB per file
	defaultLogKeep  = 3       // plus three rotated copies
	gmailHost       = "imap.gmail.com"
)

type Config struct {
	Accounts []*Account `yaml:"accounts"`
	Log      LogOptions `yaml:"log"`
}

// LogOptions bounds what mailsync.log is allowed to occupy on disk. Total usage
// stays around MaxSize * (Keep + 1).
type LogOptions struct {
	MaxSize ByteSize `yaml:"max_size"`
	Keep    *int     `yaml:"keep"`
}

type Account struct {
	Name     string      `yaml:"name"`
	Source   Endpoint    `yaml:"source"`
	Dest     Endpoint    `yaml:"dest"`
	Folders  []FolderMap `yaml:"folders"`
	Interval Duration    `yaml:"interval"`
}

type Endpoint struct {
	// Type is a shorthand: "gmail" fills in imap.gmail.com:993 over TLS.
	Type     string `yaml:"type"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	// TLS is "tls" (implicit, port 993) or "starttls" (upgrade on port 143).
	TLS string `yaml:"tls"`
	// Fingerprint pins the server's certificate by its SHA-256 digest. Set it
	// for servers whose certificate does not validate normally -- shared
	// hosting often serves a self-signed one under the node's own name. A pin
	// still detects an impostor; skipping verification would not.
	// Obtain it with: mailsync fingerprint HOST:PUERTO
	Fingerprint string `yaml:"fingerprint"`
}

func (e Endpoint) Addr() string { return fmt.Sprintf("%s:%d", e.Host, e.Port) }

// FolderMap is one source folder and where it lands on the destination. It
// accepts either a bare string ("INBOX") or a mapping ({from: X, to: Y}).
type FolderMap struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

func (f *FolderMap) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		var s string
		if err := node.Decode(&s); err != nil {
			return err
		}
		f.From, f.To = s, s
		return nil
	}
	type raw FolderMap
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*f = FolderMap(r)
	if f.To == "" {
		f.To = f.From
	}
	return nil
}

// Duration lets the YAML carry human intervals such as "2m" or "30s".
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("intervalo inválido %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) Std() time.Duration { return time.Duration(d) }

// ByteSize accepts sizes written the way people write them: 500KB, 5MB, 1GB,
// or a plain number of bytes.
type ByteSize int64

var sizeUnits = []struct {
	suffix string
	factor int64
}{
	{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10},
	{"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10}, {"B", 1},
}

func (b *ByteSize) UnmarshalYAML(node *yaml.Node) error {
	var raw string
	if err := node.Decode(&raw); err != nil {
		return err
	}
	text := strings.ToUpper(strings.TrimSpace(raw))
	for _, unit := range sizeUnits {
		if !strings.HasSuffix(text, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(strings.TrimSuffix(text, unit.suffix))
		value, err := strconv.ParseFloat(number, 64)
		if err != nil {
			return fmt.Errorf("tamaño inválido %q: %w", raw, err)
		}
		*b = ByteSize(value * float64(unit.factor))
		return nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("tamaño inválido %q: usa un número o algo como 5MB", raw)
	}
	*b = ByteSize(value)
	return nil
}

func (b ByteSize) String() string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%dB", int64(b))
	}
}

var placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// LoadConfig reads config.yaml, resolves ${VAR} placeholders against the
// environment and secrets.env, and validates the result.
func LoadConfig(p Paths) (*Config, error) {
	if err := checkPerms(p.Dir, 0o700); err != nil {
		return nil, err
	}
	if err := checkPerms(p.ConfigFile(), 0o600); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p.ConfigFile())
	if err != nil {
		return nil, err
	}
	secrets, err := loadSecrets(p)
	if err != nil {
		return nil, err
	}
	expanded, err := expand(data, secrets)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.ConfigFile(), err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", p.ConfigFile(), err)
	}
	if err := cfg.normalize(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// loadSecrets reads optional KEY=VALUE lines from secrets.env. Real environment
// variables win over the file.
func loadSecrets(p Paths) (map[string]string, error) {
	out := map[string]string{}
	path, err := p.findSecretsFile()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return out, nil
	}
	if err := checkPerms(path, 0o600); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for line := 1; scan.Scan(); line++ {
		text := strings.TrimSpace(scan.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, val, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: se esperaba CLAVE=valor", path, line)
		}
		out[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return out, scan.Err()
}

// expand substitutes ${VAR} inside the configuration's values. It works on the
// parsed document rather than the raw text so that a placeholder mentioned in a
// comment is left alone.
func expand(data []byte, secrets map[string]string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Kind == 0 {
		return data, nil // empty document
	}

	var missing []string
	lookup := func(name string) (string, bool) {
		if v, ok := os.LookupEnv(name); ok {
			return v, true
		}
		v, ok := secrets[name]
		return v, ok
	}

	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.ScalarNode {
			substituted := false
			n.Value = placeholder.ReplaceAllStringFunc(n.Value, func(match string) string {
				name := placeholder.FindStringSubmatch(match)[1]
				if v, ok := lookup(name); ok {
					substituted = true
					return v
				}
				missing = append(missing, name)
				return match
			})
			if substituted {
				// A substituted value is plain data, never YAML to
				// re-interpret: a password of "yes" must stay a password.
				n.Tag = "!!str"
				n.Style = yaml.DoubleQuotedStyle
			}
			return
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	walk(&root)

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("variables sin definir en el entorno ni en secrets.env: %s",
			strings.Join(unique(missing), ", "))
	}
	return yaml.Marshal(&root)
}

func unique(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// normalize applies defaults and rejects configurations that cannot work.
func (c *Config) normalize() error {
	if len(c.Accounts) == 0 {
		return fmt.Errorf("no hay ninguna cuenta definida en accounts:")
	}
	if c.Log.MaxSize == 0 {
		c.Log.MaxSize = defaultLogSize
	}
	if c.Log.MaxSize < 1<<10 {
		return fmt.Errorf("log.max_size demasiado pequeño: %s", c.Log.MaxSize)
	}
	if c.Log.Keep == nil {
		keep := defaultLogKeep
		c.Log.Keep = &keep
	}
	if *c.Log.Keep < 0 {
		return fmt.Errorf("log.keep no puede ser negativo")
	}
	names := map[string]bool{}
	for i, a := range c.Accounts {
		if a.Name == "" {
			return fmt.Errorf("accounts[%d]: falta name", i)
		}
		if names[a.Name] {
			return fmt.Errorf("cuenta duplicada: %q", a.Name)
		}
		names[a.Name] = true

		if a.Interval <= 0 {
			a.Interval = Duration(defaultInterval)
		}
		if len(a.Folders) == 0 {
			a.Folders = []FolderMap{{From: "INBOX", To: "INBOX"}}
		}
		for j, f := range a.Folders {
			if f.From == "" {
				return fmt.Errorf("%s: folders[%d] está vacío", a.Name, j)
			}
			if f.To == "" {
				a.Folders[j].To = f.From
			}
		}
		if err := a.Source.normalize(a.Name, "source"); err != nil {
			return err
		}
		if err := a.Dest.normalize(a.Name, "dest"); err != nil {
			return err
		}
	}
	return nil
}

func (e *Endpoint) normalize(account, side string) error {
	where := fmt.Sprintf("%s/%s", account, side)
	if strings.EqualFold(e.Type, "gmail") && e.Host == "" {
		e.Host = gmailHost
	}
	if e.Host == "" {
		return fmt.Errorf("%s: falta host", where)
	}
	if e.Port == 0 {
		e.Port = defaultPort
	}
	if e.TLS == "" {
		if e.Port == 143 {
			e.TLS = "starttls"
		} else {
			e.TLS = "tls"
		}
	}
	if e.TLS != "tls" && e.TLS != "starttls" {
		return fmt.Errorf("%s: tls debe ser \"tls\" o \"starttls\", no %q", where, e.TLS)
	}
	if e.User == "" {
		return fmt.Errorf("%s: falta user", where)
	}
	// An absent password is not an error: it means "ask me when you start".
	// See resolvePasswords.
	if e.Fingerprint != "" {
		normalized, err := normalizeFingerprint(e.Fingerprint)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		e.Fingerprint = normalized
	}
	e.Password = e.cleanPassword(e.Password)
	return nil
}

// cleanPassword tidies a password however it arrived -- from the config, from
// secrets.env or typed in. Gmail shows app passwords in groups of four, and
// they must be sent joined.
func (e Endpoint) cleanPassword(password string) string {
	if strings.EqualFold(e.Type, "gmail") || e.Host == gmailHost {
		return strings.ReplaceAll(password, " ", "")
	}
	return password
}

// normalizeFingerprint accepts the digest with or without colons and in any
// case, and rejects anything that is not a SHA-256 hex digest.
func normalizeFingerprint(in string) (string, error) {
	clean := strings.ToLower(strings.NewReplacer(":", "", " ", "", "-", "").Replace(in))
	if len(clean) != 64 {
		return "", fmt.Errorf("fingerprint inválido: se esperaban 64 caracteres hex (SHA-256), hay %d", len(clean))
	}
	if _, err := hex.DecodeString(clean); err != nil {
		return "", fmt.Errorf("fingerprint inválido: %w", err)
	}
	return clean, nil
}
