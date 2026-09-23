package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func writeConfig(t *testing.T, body string) Paths {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := Paths{Dir: dir}
	if err := os.WriteFile(p.ConfigFile(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimalConfig = `
accounts:
  - name: trabajo
    source:
      host: mail.midominio.com
      user: usuario@midominio.com
      password: ${TRABAJO_PASS}
    dest:
      type: gmail
      user: cuenta@gmail.com
      password: ${GMAIL_APP_PASS}
    folders:
      - INBOX
      - from: INBOX.Sent
        to: midominio/Enviados
    interval: 2m
`

func TestLoadConfigAppliesDefaultsAndExpandsSecrets(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	t.Setenv("TRABAJO_PASS", "secreto-origen")
	t.Setenv("GMAIL_APP_PASS", "abcd efgh ijkl mnop")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	acc := cfg.Accounts[0]

	if acc.Source.Password != "secreto-origen" {
		t.Errorf("no se expandió la contraseña del origen: %q", acc.Source.Password)
	}
	if acc.Source.Port != 993 || acc.Source.TLS != "tls" {
		t.Errorf("valores por defecto del origen incorrectos: %d %s", acc.Source.Port, acc.Source.TLS)
	}
	if acc.Dest.Host != gmailHost {
		t.Errorf("type: gmail no rellenó el host: %q", acc.Dest.Host)
	}
	// Google shows app passwords in groups of four; they must be sent joined.
	if acc.Dest.Password != "abcdefghijklmnop" {
		t.Errorf("no se quitaron los espacios de la contraseña de aplicación: %q", acc.Dest.Password)
	}
	if acc.Interval.Std() != 2*time.Minute {
		t.Errorf("intervalo %v", acc.Interval.Std())
	}
	want := []FolderMap{{From: "INBOX", To: "INBOX"}, {From: "INBOX.Sent", To: "midominio/Enviados"}}
	for i, f := range want {
		if acc.Folders[i] != f {
			t.Errorf("folders[%d] = %+v, se esperaba %+v", i, acc.Folders[i], f)
		}
	}
}

func TestLoadConfigReadsSecretsFile(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	secrets := "# comentario\nTRABAJO_PASS=desde-fichero\nGMAIL_APP_PASS=\"otra\"\n"
	if err := os.WriteFile(p.SecretsFile(), []byte(secrets), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Accounts[0].Source.Password; got != "desde-fichero" {
		t.Errorf("password = %q", got)
	}
	if got := cfg.Accounts[0].Dest.Password; got != "otra" {
		t.Errorf("no se quitaron las comillas: %q", got)
	}
}

func TestLoadConfigReportsMissingVariables(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("se esperaba un error por variables sin definir")
	}
	for _, name := range []string{"TRABAJO_PASS", "GMAIL_APP_PASS"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("el error no menciona %s: %v", name, err)
		}
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	p := writeConfig(t, "accounts:\n  - name: x\n    hsot: typo\n")
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("se esperaba un error por un campo desconocido")
	}
}

func TestLoadConfigRejectsDuplicateAccounts(t *testing.T) {
	body := strings.ReplaceAll(minimalConfig, "${TRABAJO_PASS}", "a")
	body = strings.ReplaceAll(body, "${GMAIL_APP_PASS}", "b")
	p := writeConfig(t, body+strings.TrimPrefix(body, "\naccounts:"))
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("se esperaba un error por cuenta duplicada")
	}
}

// Credentials sit in these files in the clear, so mailsync refuses to run when
// anyone other than the owner can read them.
func TestLoadConfigRefusesWorldReadableFiles(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	t.Setenv("TRABAJO_PASS", "x")
	t.Setenv("GMAIL_APP_PASS", "y")
	if err := os.Chmod(p.ConfigFile(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(p)
	if err == nil {
		t.Fatal("se esperaba un error por permisos abiertos")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("el error no explica cómo arreglarlo: %v", err)
	}
}

func TestResolveDirPrecedence(t *testing.T) {
	t.Setenv("MAILSYNC_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")

	if p, _ := resolveDir(""); p.Dir != "/tmp/xdg/mailsync" {
		t.Errorf("XDG_CONFIG_HOME ignorado: %s", p.Dir)
	}
	t.Setenv("MAILSYNC_HOME", "/tmp/home")
	if p, _ := resolveDir(""); p.Dir != "/tmp/home" {
		t.Errorf("MAILSYNC_HOME ignorado: %s", p.Dir)
	}
	if p, _ := resolveDir("/tmp/flag"); p.Dir != "/tmp/flag" {
		t.Errorf("--config ignorado: %s", p.Dir)
	}
}

// The first run must leave behind something editable rather than starting up
// half-configured.
func TestCreateWritesUsableExample(t *testing.T) {
	p := Paths{Dir: filepath.Join(t.TempDir(), "mailsync")}
	created, err := p.Create()
	if err != nil || !created {
		t.Fatalf("Create() = %v, %v", created, err)
	}
	info, err := os.Stat(p.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config.yaml creado con permisos %04o", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(p.Dir)
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("directorio creado con permisos %04o", dirInfo.Mode().Perm())
	}

	// The shipped example must itself be valid once its variables are set.
	t.Setenv("TRABAJO_PASS", "a")
	t.Setenv("GMAIL_APP_PASS", "b")
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("el config.yaml de ejemplo no es válido: %v", err)
	}

	again, err := p.Create()
	if err != nil || again {
		t.Errorf("Create() sobre uno existente = %v, %v", again, err)
	}
}

// The expansion runs over parsed values, so a placeholder written inside a
// comment is documentation, not a variable to resolve.
func TestPlaceholdersInCommentsAreIgnored(t *testing.T) {
	body := "# usa ${CUALQUIER_COSA} para tus secretos\n" +
		strings.ReplaceAll(strings.ReplaceAll(minimalConfig,
			"${TRABAJO_PASS}", "literal"), "${GMAIL_APP_PASS}", "otra")
	p := writeConfig(t, body)
	if _, err := LoadConfig(p); err != nil {
		t.Fatalf("un ${...} en un comentario rompió la carga: %v", err)
	}
}

// A substituted value is data. A password that happens to read as a YAML
// boolean or number must survive as the string it is.
func TestSubstitutedValuesStayStrings(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	t.Setenv("TRABAJO_PASS", "yes")
	t.Setenv("GMAIL_APP_PASS", "0123456789")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Accounts[0].Source.Password; got != "yes" {
		t.Errorf("password = %q, se esperaba \"yes\"", got)
	}
	if got := cfg.Accounts[0].Dest.Password; got != "0123456789" {
		t.Errorf("password = %q, se esperaba \"0123456789\"", got)
	}
}

func TestFingerprintNormalization(t *testing.T) {
	digest := strings.Repeat("AB:", 31) + "AB"
	got, err := normalizeFingerprint(digest)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("ab", 32) {
		t.Errorf("no se normalizó: %q", got)
	}
	for _, bad := range []string{"abc", strings.Repeat("zz", 32), ""} {
		if _, err := normalizeFingerprint(bad); err == nil {
			t.Errorf("se aceptó un fingerprint inválido: %q", bad)
		}
	}
}

func TestByteSizeParsing(t *testing.T) {
	cases := map[string]ByteSize{
		"5MB": 5 << 20, "512KB": 512 << 10, "1GB": 1 << 30,
		"2M": 2 << 20, "1024": 1024, "0.5MB": 512 << 10,
	}
	for input, want := range cases {
		var got ByteSize
		node := yaml.Node{Kind: yaml.ScalarNode, Value: input}
		if err := got.UnmarshalYAML(&node); err != nil {
			t.Errorf("%q: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("%q = %d, se esperaba %d", input, got, want)
		}
	}
	var bad ByteSize
	node := yaml.Node{Kind: yaml.ScalarNode, Value: "mucho"}
	if err := bad.UnmarshalYAML(&node); err == nil {
		t.Error("se aceptó un tamaño inválido")
	}
}

func TestLogDefaultsAndLimits(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	t.Setenv("TRABAJO_PASS", "a")
	t.Setenv("GMAIL_APP_PASS", "b")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.MaxSize != defaultLogSize || *cfg.Log.Keep != defaultLogKeep {
		t.Errorf("valores por defecto del log: %s x %d", cfg.Log.MaxSize, *cfg.Log.Keep)
	}

	// keep: 0 is a deliberate choice (no backups), not an unset field.
	p2 := writeConfig(t, minimalConfig+"\nlog:\n  max_size: 2MB\n  keep: 0\n")
	cfg2, err := LoadConfig(p2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Log.MaxSize != 2<<20 || *cfg2.Log.Keep != 0 {
		t.Errorf("log: %s x %d", cfg2.Log.MaxSize, *cfg2.Log.Keep)
	}

	p3 := writeConfig(t, minimalConfig+"\nlog:\n  max_size: 10B\n")
	if _, err := LoadConfig(p3); err == nil {
		t.Error("se aceptó un max_size absurdo")
	}
}

// ".env" is what most people reach for, so it is accepted alongside
// "secrets.env".
func TestDotEnvIsAcceptedAsSecretsFile(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	if err := os.WriteFile(p.DotEnvFile(), []byte("TRABAJO_PASS=desde-dotenv\nGMAIL_APP_PASS=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Accounts[0].Source.Password; got != "desde-dotenv" {
		t.Errorf("password = %q", got)
	}
}

// When both exist, secrets.env is the one that counts.
func TestSecretsEnvWinsOverDotEnv(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	if err := os.WriteFile(p.DotEnvFile(), []byte("TRABAJO_PASS=dotenv\nGMAIL_APP_PASS=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.SecretsFile(), []byte("TRABAJO_PASS=secretsenv\nGMAIL_APP_PASS=y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Accounts[0].Source.Password; got != "secretsenv" {
		t.Errorf("password = %q", got)
	}
}

// A .env with open permissions is as dangerous as an open secrets.env.
func TestDotEnvPermissionsAreChecked(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	if err := os.WriteFile(p.DotEnvFile(), []byte("TRABAJO_PASS=a\nGMAIL_APP_PASS=b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(p); err == nil {
		t.Fatal("se aceptó un .env legible por todos")
	}
}
