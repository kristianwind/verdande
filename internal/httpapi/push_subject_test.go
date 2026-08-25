package httpapi

import (
	"testing"

	"github.com/kristianwind/verdande/internal/config"
)

// The VAPID subject has to be one a push service will accept. Apple answers 403 to
// a mailto with a made-up domain, which is what the no-mail fallback used to be, so
// every push to an Apple device was refused. With no real address, the instance's
// own https URL is used instead — valid, real, and accepted.
func TestVAPIDSubject(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		baseURL string
		want    string
	}{
		{"a real address is used as-is", "todo@example.dk", "https://verdande.example.dk", "mailto:todo@example.dk"},
		{"no mail falls back to the https url", "", "https://verdande.example.dk", "https://verdande.example.dk"},
		{"the localhost default is not a real address", "verdande@localhost", "https://verdande.example.dk", "https://verdande.example.dk"},
		{"a plain http dev url still serves as a subject", "", "http://localhost:8080", "http://localhost:8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: &config.Config{BaseURL: tc.baseURL, SMTP: config.SMTP{From: tc.from}}}
			if got := s.vapidSubject(); got != tc.want {
				t.Errorf("vapidSubject() = %q, want %q", got, tc.want)
			}
		})
	}
}
