package imap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// The round trip, against a real IMAP server rather than a stub of one. The
// earlier tests cover the pure parts — what a sender renders as, where a snippet
// is cut — and would all have passed with the fetch itself broken.
func TestFlaggedMailComesBackAsMessages(t *testing.T) {
	addr, pool := serveMailbox(t, []mail{
		{subject: "Faktura 4711", from: "Anders Bo <anders@example.dk>", body: "Vedhæftet.", flagged: true},
		{subject: "Nyhedsbrev", from: "noreply@example.com", body: "Læs mere", flagged: false},
		{subject: "Kontrakt", from: "jura@example.dk", body: "Til underskrift", flagged: true},
	})

	client, err := Dial(Account{
		Host: addr, Username: "kw", Password: "hemmelig", RootCAs: pool,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	msgs, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}

	// Two of the three. The newsletter is unflagged, and flagging is the whole
	// signal: unread is a state the owner is still using.
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want the 2 flagged ones", len(msgs))
	}
	if msgs[0].Subject != "Faktura 4711" {
		t.Errorf("first subject is %q", msgs[0].Subject)
	}
	if !strings.Contains(msgs[0].From, "anders@example.dk") {
		t.Errorf("first sender is %q", msgs[0].From)
	}
	if msgs[0].UID == 0 {
		t.Error("no uid came back, so a second run would fetch everything again")
	}

	// And reading on from a marker returns only what is newer, which is the whole
	// of the deduplication.
	rest, err := client.Since(msgs[0].UID, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].Subject != "Kontrakt" {
		t.Fatalf("reading on from uid %d gave %d messages", msgs[0].UID, len(rest))
	}
}

func TestAWrongPasswordIsRefusedClearly(t *testing.T) {
	addr, pool := serveMailbox(t, nil)
	_, err := Dial(Account{Host: addr, Username: "kw", Password: "forkert", RootCAs: pool})
	if err == nil {
		t.Fatal("a wrong password was accepted")
	}
	// Hosts say everything from AUTHENTICATIONFAILED to a sentence about the web,
	// so the reply names the host and the user rather than repeating their words.
	if !strings.Contains(err.Error(), "kw") {
		t.Errorf("the error does not say whose login was refused: %v", err)
	}
}

// A certificate this process signed itself must not be enough on its own.
func TestAnUntrustedCertificateIsRefused(t *testing.T) {
	addr, _ := serveMailbox(t, nil)
	if _, err := Dial(Account{Host: addr, Username: "kw", Password: "hemmelig"}); err == nil {
		t.Fatal("a self-signed certificate was accepted without being trusted")
	}
}

type mail struct {
	subject, from, body string
	flagged             bool
}

// serveMailbox starts an in-process IMAP server over TLS and returns its address
// and the pool that trusts it.
func serveMailbox(t *testing.T, mails []mail) (string, *x509.CertPool) {
	t.Helper()

	cert, pool := selfSigned(t)
	memServer := imapmemserver.New()
	user := imapmemserver.NewUser("kw", "hemmelig")
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	memServer.AddUser(user)

	for _, m := range mails {
		flags := []imap.Flag{}
		if m.flagged {
			flags = append(flags, imap.FlagFlagged)
		}
		raw := "From: " + m.from + "\r\nSubject: " + m.subject +
			"\r\nDate: Tue, 18 Aug 2026 10:00:00 +0200\r\n\r\n" + m.body + "\r\n"
		if _, err := user.Append("INBOX", strings.NewReader(raw), &imap.AppendOptions{
			Flags: flags,
		}); err != nil {
			t.Fatal(err)
		}
	}

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memServer.NewSession(), nil, nil
		},
		TLSConfig:    &tls.Config{Certificates: []tls.Certificate{cert}},
		InsecureAuth: false,
	})
	t.Cleanup(func() { srv.Close() })

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	// "localhost", not the loopback address: the certificate names a host, and the
	// client checks that name — as it should.
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return net.JoinHostPort("localhost", port), pool
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return cert, pool
}
