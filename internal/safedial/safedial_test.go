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

// Tailscale skal virke, og på begge familier.
//
// IPv4-halvdelen slap allerede igennem, men ved et tilfælde: Gos IsPrivate
// dækker RFC1918 og ikke det CGNAT-område, Tailscale låner. IPv6-halvdelen gjorde
// ikke — fd7a:115c:a1e0::/48 ligger inde i fc00::/7. Samme maskine var altså
// tilgængelig eller ej alt efter, hvilken adressefamilie man ramte den på.
//
// Testen står her for at holde begge dele fast: at 100.x er tilladt med vilje og
// ikke af held, og at fd7a: ikke ryger tilbage bag muren, næste gang nogen rydder
// op i prædikatet.
func TestTailnetIsNotTheInside(t *testing.T) {
	for _, s := range []string{
		"100.64.0.0", "100.72.154.85", "100.127.255.255",
		"fd7a:115c:a1e0::1", "fd7a:115c:a1e0:ab12:3456:7890:abcd:ef01",
	} {
		if Blocked(net.ParseIP(s)) {
			t.Errorf("Blocked(%s) = true, men det er en tailnet-adresse", s)
		}
		if !IsTailscale(net.ParseIP(s)) {
			t.Errorf("IsTailscale(%s) = false", s)
		}
	}

	// Undtagelsen skal være så smal, som den er navngivet. Naboerne til området
	// er ikke Tailscale, og en anden unique-local-adresse er stadig indenfor.
	for _, s := range []string{
		"100.63.255.255", // lige under 100.64.0.0/10
		"100.128.0.0",    // lige over
		"fd00::1",        // unique-local, men ikke Tailscales
		"fd7b:115c:a1e0::1",
		"192.168.1.150",
	} {
		if IsTailscale(net.ParseIP(s)) {
			t.Errorf("IsTailscale(%s) = true, men den ligger uden for området", s)
		}
	}
	// Og de af dem, der er indenfor, skal stadig være det.
	for _, s := range []string{"fd00::1", "fd7b:115c:a1e0::1", "192.168.1.150"} {
		if !Blocked(net.ParseIP(s)) {
			t.Errorf("Blocked(%s) = false — muren er væk for mere end Tailscale", s)
		}
	}
}
