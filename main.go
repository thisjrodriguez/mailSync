package main

import (
	"context"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/emersion/go-imap/v2"
)

const version = "0.1.0"

const usage = `mailsync - copia correo de un servidor IMAP a otro (Gmail incluido)

Uso:
  mailsync <comando> [--config RUTA]

Comandos:
  init      crea el directorio de trabajo y un config.yaml de ejemplo
  folders   lista las carpetas del buzón de origen de cada cuenta
  check     valida la configuración y prueba el login, sin copiar nada
  run       copia en bucle, según el intervalo de cada cuenta
  once      hace una sola pasada por todas las cuentas y termina
  status    muestra lo sincronizado hasta ahora
  fingerprint HOST[:PUERTO]
            muestra el certificado que presenta un servidor, para fijarlo
  version   imprime la versión

El directorio de trabajo se resuelve en este orden:
  --config, $MAILSYNC_HOME, $XDG_CONFIG_HOME/mailsync, ~/.config/mailsync
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mailsync: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fs := flag.NewFlagSet("mailsync", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	configDir := fs.String("config", "", "directorio de trabajo")

	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("falta el comando")
	}
	command := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	if command == "version" {
		fmt.Println("mailsync " + version)
		return nil
	}
	if command == "fingerprint" {
		if fs.NArg() != 1 {
			return fmt.Errorf("uso: mailsync fingerprint HOST[:PUERTO]")
		}
		return cmdFingerprint(fs.Arg(0))
	}

	paths, err := resolveDir(*configDir)
	if err != nil {
		return err
	}

	switch command {
	case "init":
		return cmdInit(paths)
	case "check":
		return cmdCheck(paths)
	case "run":
		return cmdRun(paths, false)
	case "once":
		return cmdRun(paths, true)
	case "status":
		return cmdStatus(paths)
	case "folders":
		return cmdFolders(paths)
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("comando desconocido: %q", command)
	}
}

func cmdInit(p Paths) error {
	created, err := p.Create()
	if err != nil {
		return err
	}
	if !created {
		fmt.Printf("Ya existe una configuración en %s\n", p.ConfigFile())
		return nil
	}
	fmt.Printf("Creado %s\n\nEdítalo y después ejecuta:\n  mailsync check\n", p.ConfigFile())
	return nil
}

// loadOrGuide loads the config, creating the directory and bailing out with
// instructions the first time round rather than starting half-configured.
func loadOrGuide(p Paths) (*Config, error) {
	if _, err := os.Stat(p.ConfigFile()); os.IsNotExist(err) {
		if _, err := p.Create(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("he creado tu configuración en %s\n  edítala y vuelve a lanzarme", p.ConfigFile())
	}
	cfg, err := LoadConfig(p)
	if err != nil {
		return nil, err
	}
	// Any password left out of the config is asked for now, once, before a
	// single connection is attempted.
	if err := resolvePasswords(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func cmdCheck(p Paths) error {
	cfg, err := loadOrGuide(p)
	if err != nil {
		return err
	}
	fmt.Printf("Configuración: %s\n%d cuenta(s)\n\n", p.ConfigFile(), len(cfg.Accounts))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var failed int
	for _, acc := range cfg.Accounts {
		fmt.Printf("[%s]\n", acc.Name)
		if err := checkAccount(ctx, acc); err != nil {
			failed++
			fmt.Printf("  ERROR %v\n\n", err)
			continue
		}
		fmt.Println()
	}
	if failed > 0 {
		return fmt.Errorf("%d de %d cuentas con problemas", failed, len(cfg.Accounts))
	}
	fmt.Println("Todo correcto.")
	return nil
}

func checkAccount(ctx context.Context, acc *Account) error {
	src, err := connect(ctx, acc.Source)
	if err != nil {
		return fmt.Errorf("origen: %w", err)
	}
	defer logout(src)
	fmt.Printf("  origen   %s  OK\n", acc.Source.Addr())

	dst, err := connect(ctx, acc.Dest)
	if err != nil {
		return fmt.Errorf("destino: %w", err)
	}
	defer logout(dst)
	fmt.Printf("  destino  %s  OK\n", acc.Dest.Addr())

	for _, folder := range acc.Folders {
		sel, err := src.Select(folder.From, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			return fmt.Errorf("no se puede abrir %q en el origen: %w", folder.From, err)
		}
		fmt.Printf("  carpeta  %-28s %6d mensajes  ->  %s\n",
			folder.From, sel.NumMessages, folder.To)
	}
	fmt.Printf("  intervalo %s\n", acc.Interval.Std())
	return nil
}

func cmdRun(p Paths, once bool) error {
	cfg, err := loadOrGuide(p)
	if err != nil {
		return err
	}
	store, err := OpenStore(p.StateFile())
	if err != nil {
		return err
	}
	defer store.Close()

	logFile, err := newRotatingWriter(p.LogFile(), int64(cfg.Log.MaxSize), *cfg.Log.Keep)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := log.New(io.MultiWriter(os.Stdout, logFile), "", log.LstdFlags)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Printf("mailsync %s arrancando con %d cuenta(s); log limitado a %s x %d copias",
		version, len(cfg.Accounts), cfg.Log.MaxSize, *cfg.Log.Keep+1)

	var wg sync.WaitGroup
	for _, acc := range cfg.Accounts {
		wg.Add(1)
		go func(acc *Account) {
			defer wg.Done()
			syncer := &Syncer{account: acc, store: store, log: logger}
			runAccount(ctx, syncer, once)
		}(acc)
	}
	wg.Wait()

	if ctx.Err() != nil {
		logger.Printf("parada solicitada, saliendo")
	}
	return nil
}

// runAccount drives one account's loop. A failed pass is logged and retried on
// the next tick; it never takes down the other accounts.
func runAccount(ctx context.Context, s *Syncer, once bool) {
	for {
		start := time.Now()
		if err := s.Run(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Printf("[%s] pasada fallida tras %s: %v", s.account.Name, time.Since(start).Round(time.Second), err)
		}
		if once {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.account.Interval.Std()):
		}
	}
}

// cmdFingerprint shows what a server presents so the digest can be pinned. It
// deliberately validates nothing: the point is to look at an untrusted
// certificate and decide whether to trust it.
func cmdFingerprint(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sum, cert, err := peerFingerprint(ctx, addr)
	if err != nil {
		return err
	}
	fmt.Printf("servidor    %s\n", addr)
	fmt.Printf("sujeto      %s\n", cert.Subject)
	fmt.Printf("emisor      %s\n", cert.Issuer)
	fmt.Printf("nombres     %s\n", strings.Join(certNames(cert), ", "))
	fmt.Printf("validez     %s  ->  %s\n",
		cert.NotBefore.Format("2006-01-02"), cert.NotAfter.Format("2006-01-02"))
	fmt.Printf("\nfingerprint: %s\n", sum)
	fmt.Printf("\nSi reconoces este certificado, añádelo al endpoint correspondiente:\n  fingerprint: %s\n", sum)
	return nil
}

func certNames(cert *x509.Certificate) []string {
	names := append([]string{}, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) == 0 {
		names = append(names, "(ninguno)")
	}
	return names
}

// cmdFolders lists what is actually in the source mailbox, which is how you
// build the folder list for a full migration without guessing names.
func cmdFolders(p Paths) error {
	cfg, err := loadOrGuide(p)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, acc := range cfg.Accounts {
		fmt.Printf("[%s] origen %s\n", acc.Name, acc.Source.Addr())
		client, err := connect(ctx, acc.Source)
		if err != nil {
			return err
		}
		mailboxes, err := client.List("", "*", nil).Collect()
		if err != nil {
			logout(client)
			return fmt.Errorf("no se pudieron listar las carpetas: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		var total uint32
		for _, mbox := range mailboxes {
			if slices.Contains(mbox.Attrs, imap.MailboxAttrNoSelect) {
				fmt.Fprintf(w, "  %s\t(no seleccionable)\n", mbox.Mailbox)
				continue
			}
			var count string
			if data, err := client.Status(mbox.Mailbox, &imap.StatusOptions{NumMessages: true}).Wait(); err == nil && data.NumMessages != nil {
				count = fmt.Sprintf("%d mensajes", *data.NumMessages)
				total += *data.NumMessages
			}
			fmt.Fprintf(w, "  %s\t%s\n", mbox.Mailbox, count)
		}
		w.Flush()
		fmt.Printf("  total: %d mensajes\n\n", total)
		logout(client)
	}
	return nil
}

func cmdStatus(p Paths) error {
	if _, err := os.Stat(p.StateFile()); os.IsNotExist(err) {
		fmt.Printf("Todavía no hay estado en %s (no se ha ejecutado ninguna pasada)\n", p.StateFile())
		return nil
	}
	store, err := OpenStore(p.StateFile())
	if err != nil {
		return err
	}
	defer store.Close()

	states, err := store.All()
	if err != nil {
		return err
	}
	if len(states) == 0 {
		fmt.Println("Todavía no se ha sincronizado ninguna carpeta.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CUENTA\tCARPETA\tCOPIADOS\tOMITIDOS\tÚLTIMO UID\tÚLTIMA PASADA\tESTADO")
	for _, st := range states {
		last := "nunca"
		if !st.LastRun.IsZero() {
			last = st.LastRun.Format("2006-01-02 15:04")
		}
		status := "ok"
		if st.LastError != "" {
			status = "ERROR: " + firstLine(st.LastError)
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
			st.Account, st.Folder, st.Copied, st.Skipped, st.LastUID, last, status)
	}
	return w.Flush()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
