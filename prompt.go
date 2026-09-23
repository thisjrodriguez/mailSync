package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// passwordReader is how mailsync asks for a password. Tests replace it; in
// normal use it reads from the terminal with the echo turned off.
var passwordReader = readPasswordFromTerminal

// resolvePasswords fills in any password left out of the configuration by
// asking for it once, up front. Doing it before anything else means a typo is
// caught immediately rather than three folders into a sync, and the answer is
// reused across reconnects for the life of the process.
func resolvePasswords(cfg *Config) error {
	for _, acc := range cfg.Accounts {
		endpoints := []struct {
			side string
			ep   *Endpoint
		}{{"origen", &acc.Source}, {"destino", &acc.Dest}}

		for _, e := range endpoints {
			if e.ep.Password != "" {
				continue
			}
			prompt := fmt.Sprintf("Contraseña de %s (%s, %s): ", e.ep.User, acc.Name, e.side)
			secret, err := passwordReader(prompt)
			if err != nil {
				return fmt.Errorf("%s/%s: %w", acc.Name, e.side, err)
			}
			if secret == "" {
				return fmt.Errorf("%s/%s: contraseña vacía", acc.Name, e.side)
			}
			e.ep.Password = e.ep.cleanPassword(secret)
		}
	}
	return nil
}

// readPasswordFromTerminal reads from the controlling terminal rather than
// stdin, so that a password can still be typed when the command's input is
// being piped from somewhere else.
func readPasswordFromTerminal(prompt string) (string, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return "", fmt.Errorf("no hay terminal para pedir la contraseña\n" +
				"  si mailsync corre desatendido, ponla en secrets.env o en una variable de entorno")
		}
		tty = os.Stdin
	} else {
		defer tty.Close()
	}

	fmt.Fprint(tty, prompt)
	secret, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(tty)
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la contraseña: %w", err)
	}
	return strings.TrimSpace(string(secret)), nil
}
