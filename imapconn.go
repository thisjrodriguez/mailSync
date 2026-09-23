package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

const dialTimeout = 30 * time.Second

// rootCAs overrides the certificate authorities used to verify servers. It is
// nil in normal use, which means the system trust store; the tests point it at
// their own throwaway CA.
var rootCAs *x509.CertPool

// connect opens an IMAP connection and logs in. Dialing goes through
// DialContext so that a shutdown signal interrupts a hung connect instead of
// waiting out the timeout.
func connect(ctx context.Context, e Endpoint) (*imapclient.Client, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", e.Addr())
	if err != nil {
		return nil, fmt.Errorf("no se pudo conectar a %s: %w", e.Addr(), err)
	}

	opts := &imapclient.Options{TLSConfig: tlsConfigFor(e)}

	var client *imapclient.Client
	switch e.TLS {
	case "starttls":
		client, err = imapclient.NewStartTLS(rawConn, opts)
		if err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("STARTTLS falló en %s: %w", e.Addr(), err)
		}
	default:
		tlsConn := tls.Client(rawConn, opts.TLSConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("el handshake TLS falló en %s: %w", e.Addr(), err)
		}
		client = imapclient.New(tlsConn, opts)
	}

	if err := client.Login(e.User, e.Password).Wait(); err != nil {
		client.Close()
		return nil, loginError(e, err)
	}
	return client, nil
}

// tlsConfigFor builds the TLS settings for an endpoint. With a pinned
// fingerprint the usual chain and hostname checks are replaced by an exact
// match against that certificate -- which is what lets mailsync talk to a host
// serving a self-signed certificate without lowering its guard.
func tlsConfigFor(e Endpoint) *tls.Config {
	cfg := &tls.Config{ServerName: e.Host, RootCAs: rootCAs}
	if e.Fingerprint == "" {
		return cfg
	}
	cfg.InsecureSkipVerify = true
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("el servidor no presentó ningún certificado")
		}
		got := sha256.Sum256(rawCerts[0])
		if hex.EncodeToString(got[:]) != e.Fingerprint {
			return fmt.Errorf("el certificado de %s no coincide con el fingerprint fijado\n  esperado: %s\n  recibido: %s\n  si el servidor ha cambiado de certificado, verifícalo con: mailsync fingerprint %s",
				e.Host, e.Fingerprint, hex.EncodeToString(got[:]), e.Addr())
		}
		return nil
	}
	return cfg
}

// peerFingerprint opens a TLS connection without validating anything and
// reports what the server presents, so the digest can be pinned afterwards.
func peerFingerprint(ctx context.Context, addr string) (string, *x509.Certificate, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
		addr = addr + ":993"
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	rawConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", nil, err
	}
	defer rawConn.Close()

	conn := tls.Client(rawConn, &tls.Config{ServerName: host, InsecureSkipVerify: true})
	if err := conn.HandshakeContext(ctx); err != nil {
		return "", nil, err
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", nil, fmt.Errorf("el servidor no presentó ningún certificado")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), certs[0], nil
}

// loginError turns the server's terse rejection into something actionable.
// Gmail in particular answers a revoked app password with a generic failure.
func loginError(e Endpoint, err error) error {
	msg := err.Error()
	if !strings.Contains(strings.ToUpper(msg), "AUTHENTICATIONFAILED") &&
		!strings.Contains(strings.ToLower(msg), "invalid credentials") {
		return fmt.Errorf("login rechazado en %s como %s: %w", e.Addr(), e.User, err)
	}
	hint := "revisa usuario y contraseña"
	if e.Host == gmailHost {
		hint = "usa una contraseña de aplicación (no la de tu cuenta), comprueba que no se haya revocado y que IMAP esté activado en Gmail"
	}
	return fmt.Errorf("autenticación rechazada en %s como %s: %s", e.Addr(), e.User, hint)
}

func logout(client *imapclient.Client) {
	if client == nil {
		return
	}
	if err := client.Logout().Wait(); err != nil {
		client.Close()
	}
}
