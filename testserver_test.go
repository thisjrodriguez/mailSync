package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// testCert builds a throwaway CA-signed certificate for 127.0.0.1 so the tests
// exercise the same TLS path as production instead of a plaintext shortcut.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mailsync-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// testServer is an in-memory IMAP server listening on TLS over localhost.
type testServer struct {
	endpoint Endpoint
	user     *imapmemserver.User
}

func startServer(t *testing.T, cert tls.Certificate, username, password string, mailboxes ...string) *testServer {
	t.Helper()

	mem := imapmemserver.New()
	user := imapmemserver.NewUser(username, password)
	for _, mbox := range mailboxes {
		if err := user.Create(mbox, nil); err != nil {
			t.Fatalf("create %s: %v", mbox, err)
		}
	}
	mem.AddUser(user)

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:   imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
		Logger: discardLogger{},
	})

	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := tls.NewListener(raw, &tls.Config{Certificates: []tls.Certificate{cert}})
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	_, portStr, err := net.SplitHostPort(raw.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portStr)

	return &testServer{
		endpoint: Endpoint{Host: "127.0.0.1", Port: port, User: username, Password: password, TLS: "tls"},
		user:     user,
	}
}

type discardLogger struct{}

func (discardLogger) Printf(format string, args ...interface{}) {}
