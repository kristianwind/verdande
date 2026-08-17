// Package mail sends the handful of messages verdande needs to send: invites,
// password resets, and later reminders and notifications.
//
// Written against net/smtp rather than a mail library because the requirement is
// small and fixed. What it does need to get right is the failure mode: an instance
// with no mail server configured must still work, so sending falls back to logging
// the link rather than returning an error that blocks the operation that caused it.
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/config"
)

type Sender struct {
	cfg config.SMTP
	log *slog.Logger
	// appName and baseURL appear in the body of every message.
	appName string
	baseURL string
}

func New(cfg config.SMTP, baseURL string, log *slog.Logger) *Sender {
	return &Sender{cfg: cfg, log: log, appName: "verdande", baseURL: baseURL}
}

// Configured reports whether mail can actually be sent. Handlers use it to decide
// whether to show an invite link on screen instead of promising an email.
func (s *Sender) Configured() bool { return s.cfg.Configured() }

func (s *Sender) SendInvite(ctx context.Context, to, inviterName, projectName, link string, ttl time.Duration) error {
	subject := fmt.Sprintf("%s har delt noget med dig i verdande", inviterName)
	what := "verdande"
	if projectName != "" {
		what = "projektet “" + projectName + "”"
	}
	body := fmt.Sprintf(`Hej

%s har inviteret dig til %s.

Opret din konto her:
%s

Linket virker i %s. Hvis du ikke ved, hvad det her handler om, kan du roligt slette denne mail — der sker ikke noget, hvis du ikke bruger linket.

— verdande
%s
`, inviterName, what, link, humanDuration(ttl), s.baseURL)

	return s.send(ctx, to, subject, body)
}

func (s *Sender) SendReminder(ctx context.Context, to, name, task, link string) error {
	subject := "Påmindelse: " + task
	body := fmt.Sprintf(`Hej %s

Du bad om en påmindelse om:

  %s

%s

— verdande
`, name, task, link)

	return s.send(ctx, to, subject, body)
}

func (s *Sender) SendPasswordReset(ctx context.Context, to, name, link string, ttl time.Duration) error {
	subject := "Nulstil din adgangskode i verdande"
	body := fmt.Sprintf(`Hej %s

Der er bedt om at nulstille adgangskoden til din verdande-konto.

Vælg en ny adgangskode her:
%s

Linket virker i %s og kan kun bruges én gang.

Har du ikke selv bedt om det, behøver du ikke gøre noget — din nuværende adgangskode virker stadig, og linket udløber af sig selv.

— verdande
%s
`, name, link, humanDuration(ttl), s.baseURL)

	return s.send(ctx, to, subject, body)
}

// send delivers one message, or logs the link if there is no mail server.
//
// Returning nil when unconfigured is deliberate: a single-user instance with no
// SMTP host is a supported way to run this, and a password reset must not fail
// there. The operator finds the link in the log.
func (s *Sender) send(ctx context.Context, to, subject, body string) error {
	if !s.cfg.Configured() {
		s.log.Warn("no SMTP host configured; the message was not sent",
			"to", to, "subject", subject, "body", body)
		return nil
	}

	from := s.cfg.From
	msg := buildMessage(from, to, subject, body)
	addr := net.JoinHostPort(s.cfg.Host, itoa(s.cfg.Port))

	// The dial is bounded so a mail server that accepts connections and then says
	// nothing cannot hold an HTTP handler open until its own timeout.
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", addr, err)
	}

	tlsCfg := &tls.Config{
		ServerName: s.cfg.Host,
		// Only when the operator has said so, for a self-signed internal server.
		InsecureSkipVerify: s.cfg.Insecure,
	}
	// Port 465 is TLS from the first byte; everything else starts in the clear and
	// negotiates with STARTTLS.
	if !s.cfg.StartTLS {
		conn = tls.Client(conn, tlsCfg)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: smtp handshake: %w", err)
	}
	defer c.Close()

	if s.cfg.StartTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("mail: starttls: %w", err)
			}
		} else {
			// Not fatal — a mail server on the same Docker network is a reasonable
			// place not to have TLS — but the operator should know credentials are
			// crossing in the clear.
			s.log.Warn("mail server does not offer STARTTLS; sending unencrypted", "host", s.cfg.Host)
		}
	}

	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("mail: authenticate: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mail: to: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := wc.Write([]byte(msg)); err != nil {
		wc.Close()
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("mail: close body: %w", err)
	}
	return c.Quit()
}

// buildMessage assembles the headers and body.
//
// The subject is RFC 2047 encoded because it contains Danish letters, and a raw
// "æ" in a header is not valid — it arrives as mojibake in most clients. The body
// declares UTF-8 for the same reason.
func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	// These are transactional messages sent because somebody just asked for one.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	// SMTP ends a message on a line containing a single dot, so a body line that is
	// just "." has to be escaped or it truncates everything after it.
	b.WriteString(strings.ReplaceAll(normaliseNewlines(body), "\r\n.\r\n", "\r\n..\r\n"))
	return b.String()
}

func normaliseNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

func humanDuration(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d dage", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d timer", int(d.Hours()))
	case d >= time.Hour:
		return "en time"
	default:
		return fmt.Sprintf("%d minutter", int(d.Minutes()))
	}
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
