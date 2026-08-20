package safedial

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBlockedCoversTheInside(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "0.0.0.0",
		// Skyens metadata-endepunkt. Den ene adresse, enhver SSRF sigter efter.
		"169.254.169.254",
		"10.0.0.5", "172.16.0.1", "192.168.1.10", "fd00::1", "fe80::1",
	}
	for _, s := range blocked {
		if !Blocked(net.ParseIP(s)) {
			t.Errorf("Blocked(%s) = false", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "8.8.8.8", "2606:2800:220:1:248:1893:25c8:1946"} {
		if Blocked(net.ParseIP(s)) {
			t.Errorf("Blocked(%s) = true, men det er en almindelig adresse", s)
		}
	}
}

// En httptest-server lytter på loopback, hvilket gør den til den perfekte prøve:
// den findes, den svarer, og den må ikke kunne nås.
func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("forbindelsen blev oprettet; den skulle være afvist")
	}))
	defer srv.Close()

	_, err := Client(3 * time.Second).Get(srv.URL)
	if err == nil {
		t.Fatal("en forespørgsel til loopback skal afvises")
	}
}

func TestAllowPrivateStillReaches(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := AllowPrivate(3 * time.Second).Get(srv.URL)
	if err != nil {
		t.Fatalf("den skal kunne nå det, operatøren selv har peget på: %v", err)
	}
	resp.Body.Close()
}
