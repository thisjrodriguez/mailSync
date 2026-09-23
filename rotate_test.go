package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func totalSize(t *testing.T, dir string) int64 {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	return total
}

// The whole point of the cap: a process that logs forever must not fill the
// disk. Usage stays bounded at roughly maxSize * (keep + 1).
func TestRotationBoundsDiskUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mailsync.log")
	const maxSize, keep = 1024, 2

	w, err := newRotatingWriter(path, maxSize, keep)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	line := []byte(strings.Repeat("x", 100) + "\n")
	for i := 0; i < 500; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("escritura %d: %v", i, err)
		}
	}

	if limit := int64(maxSize * (keep + 1)); totalSize(t, dir) > limit {
		t.Errorf("el log ocupa %d bytes, el límite es %d", totalSize(t, dir), limit)
	}
	for _, name := range []string{"mailsync.log", "mailsync.log.1", "mailsync.log.2"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("falta %s: %v", name, err)
		}
	}
	// keep = 2 means two backups, never a third.
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Error("se conservó una copia de más")
	}
}

// Rotation must not lose the newest entries, which are the ones you care about
// when something has just gone wrong.
func TestRotationKeepsMostRecentLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mailsync.log")
	w, err := newRotatingWriter(path, 512, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	for i := 0; i < 200; i++ {
		fmt.Fprintf(w, "línea %03d %s\n", i, strings.Repeat("y", 50))
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "línea 199") {
		t.Error("la última línea escrita no está en el log activo")
	}
}

// A single entry bigger than the cap is written whole: a truncated log line is
// worse than briefly overshooting.
func TestOversizedEntryIsNotTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mailsync.log")
	w, err := newRotatingWriter(path, 256, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	big := []byte(strings.Repeat("z", 2000) + "\n")
	n, err := w.Write(big)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(big) {
		t.Errorf("se escribieron %d bytes de %d", n, len(big))
	}
}

// Reopening must append to the existing file rather than clobber it, and must
// account for the bytes already there.
func TestWriterResumesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mailsync.log")
	if err := os.WriteFile(path, []byte("anterior\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := newRotatingWriter(path, 1<<20, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	fmt.Fprintln(w, "nueva")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "anterior") || !strings.Contains(string(data), "nueva") {
		t.Errorf("se perdió contenido al reabrir: %q", data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("el log quedó con permisos %04o", info.Mode().Perm())
	}
}

// Every account logs from its own goroutine, so rotation has to be safe under
// concurrent writes.
func TestConcurrentWritesDoNotRace(t *testing.T) {
	dir := t.TempDir()
	w, err := newRotatingWriter(filepath.Join(dir, "mailsync.log"), 2048, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	var wg sync.WaitGroup
	for account := 0; account < 8; account++ {
		wg.Add(1)
		go func(account int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				fmt.Fprintf(w, "[cuenta-%d] entrada %d %s\n", account, i, strings.Repeat("w", 40))
			}
		}(account)
	}
	wg.Wait()

	if limit := int64(2048 * 3); totalSize(t, dir) > limit {
		t.Errorf("el log ocupa %d bytes, el límite es %d", totalSize(t, dir), limit)
	}
}
