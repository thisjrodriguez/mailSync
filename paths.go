package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const appName = "mailsync"

// Paths locates everything mailsync owns on disk. Configuration and state live
// together in a single directory so that backing up one folder backs up
// everything.
type Paths struct {
	Dir string
}

// resolveDir picks the working directory, in order of precedence:
// --config flag, $MAILSYNC_HOME, $XDG_CONFIG_HOME/mailsync, ~/.config/mailsync.
func resolveDir(flagDir string) (Paths, error) {
	var dir string
	switch {
	case flagDir != "":
		dir = flagDir
	case os.Getenv("MAILSYNC_HOME") != "":
		dir = os.Getenv("MAILSYNC_HOME")
	case os.Getenv("XDG_CONFIG_HOME") != "":
		dir = filepath.Join(os.Getenv("XDG_CONFIG_HOME"), appName)
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("no se pudo determinar el home: %w", err)
		}
		dir = filepath.Join(home, ".config", appName)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, fmt.Errorf("ruta de configuración inválida %q: %w", dir, err)
	}
	return Paths{Dir: abs}, nil
}

func (p Paths) ConfigFile() string  { return filepath.Join(p.Dir, "config.yaml") }
func (p Paths) SecretsFile() string { return filepath.Join(p.Dir, "secrets.env") }
func (p Paths) StateFile() string   { return filepath.Join(p.Dir, "state.db") }
func (p Paths) LogFile() string     { return filepath.Join(p.Dir, "mailsync.log") }

// Create makes the directory (0700) and drops a commented example config if
// none exists. It reports whether the config file was created.
func (p Paths) Create() (bool, error) {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return false, fmt.Errorf("no se pudo crear %s: %w", p.Dir, err)
	}
	if err := os.Chmod(p.Dir, 0o700); err != nil {
		return false, fmt.Errorf("no se pudieron ajustar los permisos de %s: %w", p.Dir, err)
	}
	if _, err := os.Stat(p.ConfigFile()); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(p.ConfigFile(), []byte(exampleConfig), 0o600); err != nil {
		return false, fmt.Errorf("no se pudo escribir %s: %w", p.ConfigFile(), err)
	}
	return true, nil
}

// checkPerms refuses to continue when a file holding credentials is readable by
// anyone other than its owner. Verified on every start, not just at creation.
func checkPerms(path string, allowed fs.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode&^allowed != 0 {
		return fmt.Errorf("permisos demasiado abiertos en %s: %04o (esperado %04o o menos)\n  corrígelo con: chmod %04o %s",
			path, mode, allowed, allowed, path)
	}
	return nil
}
