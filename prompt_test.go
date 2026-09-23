package main

import (
	"strings"
	"testing"
)

// stubPrompt replaces the terminal reader and records what was asked.
func stubPrompt(t *testing.T, answers ...string) *[]string {
	t.Helper()
	var asked []string
	i := 0
	prev := passwordReader
	passwordReader = func(prompt string) (string, error) {
		asked = append(asked, prompt)
		if i >= len(answers) {
			t.Fatalf("se pidieron más contraseñas de las previstas: %q", prompt)
		}
		answer := answers[i]
		i++
		return answer, nil
	}
	t.Cleanup(func() { passwordReader = prev })
	return &asked
}

func TestPasswordIsAskedWhenAbsent(t *testing.T) {
	body := `
accounts:
  - name: trabajo
    source:
      host: mail.midominio.com
      user: usuario@midominio.com
    dest:
      type: gmail
      user: cuenta@gmail.com
`
	p := writeConfig(t, body)
	asked := stubPrompt(t, "clave-origen", "abcd efgh ijkl mnop")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolvePasswords(cfg); err != nil {
		t.Fatal(err)
	}

	if len(*asked) != 2 {
		t.Fatalf("se hicieron %d preguntas, se esperaban 2: %v", len(*asked), *asked)
	}
	// The prompt has to say which mailbox it means, or you cannot tell the two
	// sides apart when both are being asked for.
	if !strings.Contains((*asked)[0], "usuario@midominio.com") || !strings.Contains((*asked)[0], "origen") {
		t.Errorf("el primer prompt no identifica el buzón: %q", (*asked)[0])
	}
	if !strings.Contains((*asked)[1], "cuenta@gmail.com") || !strings.Contains((*asked)[1], "destino") {
		t.Errorf("el segundo prompt no identifica el buzón: %q", (*asked)[1])
	}

	if cfg.Accounts[0].Source.Password != "clave-origen" {
		t.Errorf("origen = %q", cfg.Accounts[0].Source.Password)
	}
	// A typed Gmail app password gets the same cleanup as a configured one.
	if cfg.Accounts[0].Dest.Password != "abcdefghijklmnop" {
		t.Errorf("destino = %q", cfg.Accounts[0].Dest.Password)
	}
}

func TestConfiguredPasswordIsNotAsked(t *testing.T) {
	p := writeConfig(t, minimalConfig)
	t.Setenv("TRABAJO_PASS", "a")
	t.Setenv("GMAIL_APP_PASS", "b")
	asked := stubPrompt(t)

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolvePasswords(cfg); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 0 {
		t.Errorf("se preguntó por una contraseña ya configurada: %v", *asked)
	}
}

func TestEmptyTypedPasswordIsRejected(t *testing.T) {
	body := "accounts:\n  - name: x\n    source:\n      host: h\n      user: u\n    dest:\n      host: h2\n      user: u2\n"
	p := writeConfig(t, body)
	stubPrompt(t, "")

	cfg, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolvePasswords(cfg); err == nil {
		t.Fatal("una contraseña vacía debería rechazarse")
	}
}
