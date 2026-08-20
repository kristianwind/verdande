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

	msgs, _, err := client.Since(0, 25)
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
	rest, _, err := client.Since(msgs[0].UID, 25)
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
	// raw replaces the whole message when set, which is the only way to get a
	// genuinely multipart one in.
	raw string
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
		raw := m.raw
		if raw == "" {
			raw = "From: " + m.from + "\r\nSubject: " + m.subject +
				"\r\nDate: Tue, 18 Aug 2026 10:00:00 +0200\r\n\r\n" + m.body + "\r\n"
		}
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

// The bug this was written for: every press of "Fetch now" made the same mail
// into a task again. The marker the next run starts from is the uid, and if it
// comes back as zero the marker never moves.
func TestEveryMessageCarriesAUID(t *testing.T) {
	addr, pool := serveMailbox(t, []mail{
		{subject: "En", from: "a@example.dk", body: "en", flagged: true},
		{subject: "To", from: "b@example.dk", body: "to", flagged: true},
	})
	client, err := Dial(Account{Host: addr, Username: "kw", Password: "hemmelig", RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	msgs, _, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if m.UID == 0 {
			t.Fatalf("%q came back without a uid; the marker would never move and "+
				"every run would make it into a task again", m.Subject)
		}
	}
	if msgs[0].UID == msgs[1].UID {
		t.Error("two messages share a uid")
	}
}

// The other half of the same report: the snippet under a task's title read
// "--=_be71cb39… Content-Transfer-Encoding: 8bit Content-Type: text/plain".
func TestAMultipartMailReadsAsItsText(t *testing.T) {
	const boundary = "=_be71cb3975bdfbc60a16327cb2f49c23"
	body := "This is a multi-part message.\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"Content-Type: text/plain; charset=UTF-8; format=flowed\r\n\r\n" +
		"Hej igen,\r\n\r\nVi har normalt 3 slags. :)\r\n" +
		"--" + boundary + "\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n\r\n" +
		"<html><body><p>Hej igen</p></body></html>\r\n" +
		"--" + boundary + "--\r\n"

	addr, pool := serveMailbox(t, []mail{{
		flagged: true,
		raw: "Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n" +
			"From: kontakt@mikrobagt.dk\r\nSubject: Re: Kaffebestilling\r\n" +
			"Date: Tue, 18 Aug 2026 10:00:00 +0200\r\n\r\n" + body,
	}})

	client, err := Dial(Account{Host: addr, Username: "kw", Password: "hemmelig", RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	msgs, _, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages", len(msgs))
	}

	got := msgs[0].Snippet
	for _, junk := range []string{boundary, "Content-Transfer-Encoding", "Content-Type", "<html>", "<p>"} {
		if strings.Contains(got, junk) {
			t.Errorf("the snippet still carries %q: %q", junk, got)
		}
	}
	if !strings.HasPrefix(got, "Hej igen") {
		t.Errorf("the snippet does not start with the text of the mail: %q", got)
	}
}

// The marker is ours, not the server's.
//
// A mailbox read with a marker above everything in it must come back empty. The
// query says so, but a server is free to read the range differently — one reads
// `1272:*` as `0:1272` and hands back the lot, which is how the same mail became
// a task every ten minutes while the marker stood still at 1271. The filter is
// on this side now, so the invariant holds whatever comes back.
func TestNothingAtOrBelowTheMarkerComesBack(t *testing.T) {
	addr, pool := serveMailbox(t, []mail{
		{subject: "En", from: "a@example.dk", body: "en", flagged: true},
		{subject: "To", from: "b@example.dk", body: "to", flagged: true},
		{subject: "Tre", from: "c@example.dk", body: "tre", flagged: true},
	})
	client, err := Dial(Account{Host: addr, Username: "kw", Password: "hemmelig", RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	all, highest, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d messages on the first read, want 3", len(all))
	}

	// Read again from the marker the first run would have written.
	rest, again, err := client.Since(highest, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Errorf("%d message(s) came back at or below the marker: %v", len(rest), rest[0].Subject)
	}
	// And the marker must not go backwards, or the next run reads them all again.
	if again < highest {
		t.Errorf("the marker moved back from %d to %d", highest, again)
	}

	// Every message, individually: none of them may reappear once passed.
	for _, m := range all {
		later, _, err := client.Since(m.UID, 25)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range later {
			if l.UID <= m.UID {
				t.Errorf("reading from %d gave %d back", m.UID, l.UID)
			}
		}
	}
}

// Den samme mail må ikke kunne blive en opgave to gange.
//
// Prøven kører hele vejen imod en rigtig server: den finder de flagede mails,
// tager flaget af dem, og spørger så forfra som en ny synkronisering ville — med
// markøren sat til nul, altså det værst tænkelige tilfælde, hvor alt, hvad vi
// selv havde husket, er væk. Der skal ikke komme noget tilbage.
//
// Det er det, markøren ikke kunne love. Den holdt kun, hvis uids var, hvad
// serveren sagde, og fejlede på et fetch-svar uden uid, på en UIDVALIDITY, der
// skiftede, og på to postkasse-rækker, der pegede samme sted og hver især førte
// deres eget regnskab. Alle tre gav det samme: mailen blev en opgave igen.
func TestAnUnflaggedMailDoesNotComeBack(t *testing.T) {
	addr, pool := serveMailbox(t, []mail{
		{subject: "Faktura 4711", from: "anders@example.dk", body: "Vedhæftet.", flagged: true},
		{subject: "Nyhedsbrev", from: "noreply@example.com", body: "Læs mere", flagged: false},
		{subject: "Kontrakt", from: "jura@example.dk", body: "Til underskrift", flagged: true},
	})

	client, err := Dial(Account{Host: addr, Username: "kw", Password: "hemmelig", RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	msgs, _, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("fandt %d flagede mails, ventede 2", len(msgs))
	}

	uids := make([]uint32, 0, len(msgs))
	for _, m := range msgs {
		uids = append(uids, m.UID)
	}
	if err := client.Unflag(uids); err != nil {
		t.Fatalf("Unflag: %v", err)
	}

	// Forfra, og fra nul: selv en instans, der intet husker, må ikke finde dem igen.
	again, _, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("%d mails kom igen efter at flaget var taget af — den første af dem er %q",
			len(again), again[0].Subject)
	}
}

// Et tomt kald må ikke åbne mappen skrivbar for ingenting.
func TestUnflagWithNothingToDoIsQuiet(t *testing.T) {
	addr, pool := serveMailbox(t, []mail{
		{subject: "Faktura", from: "a@example.dk", body: "x", flagged: true},
	})
	client, err := Dial(Account{Host: addr, Username: "kw", Password: "hemmelig", RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if err := client.Unflag(nil); err != nil {
		t.Errorf("Unflag(nil) = %v", err)
	}
	// Kun nuller: der er intet at adressere, og mailen skal beholde sit flag.
	if err := client.Unflag([]uint32{0, 0}); err != nil {
		t.Errorf("Unflag(nuller) = %v", err)
	}
	msgs, _, err := client.Since(0, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Errorf("mailen mistede sit flag, uden at nogen havde adresseret den")
	}
}
