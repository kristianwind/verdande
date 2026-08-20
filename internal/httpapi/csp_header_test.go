package httpapi

import (
	"strings"
	"testing"
)

// Politikken skal stå i svaret, og den skal stadig være stram.
//
// Den er dét, der gjorde en XSS i noteeditoren til indsat HTML frem for kørt
// script, dengang en `alt` slap uden om sin undslupning — og der var ingen prøve
// på, at headeren overhovedet blev sendt. En sikkerhedsforanstaltning, ingen
// prøve rører, er en, der kan forsvinde i en oprydning uden at nogen opdager det.
func TestEveryResponseCarriesAStrictCSP(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "GET", "/api/v1/auth/me", nil)
	policy := resp.Header.Get("Content-Security-Policy")
	if policy == "" {
		t.Fatal("der er ingen Content-Security-Policy på svaret")
	}

	// Målt på script-src alene, og ikke på hele politikken.
	//
	// `style-src 'unsafe-inline'` står der med vilje og er forklaret i csp.go:
	// Svelte skriver stilarter ind i dokumentet, og en indsat stil kan gøre en
	// side grim, ikke køre kode. Det er script-src, der afgør, om indsat HTML
	// bliver til et kørende script, og det er derfor den, prøven ser på.
	scriptSrc := ""
	for _, part := range strings.Split(policy, ";") {
		if strings.HasPrefix(strings.TrimSpace(part), "script-src") {
			scriptSrc = part
		}
	}
	if scriptSrc == "" {
		t.Fatal("der er ingen script-src i politikken")
	}
	for _, forbidden := range []string{"'unsafe-inline'", "'unsafe-eval'", "'unsafe-hashes'", "'strict-dynamic'"} {
		if strings.Contains(scriptSrc, forbidden) {
			t.Errorf("script-src indeholder %s: %q", forbidden, strings.TrimSpace(scriptSrc))
		}
	}

	// Og de fire, der skal være der.
	for _, want := range []string{"script-src", "object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("politikken mangler %q", want)
		}
	}
}

// Et bilag må ikke kunne blive læst som et dokument.
func TestAttachmentsAreNotServedAsDocuments(t *testing.T) {
	for _, mime := range []string{"image/svg+xml", "text/html", "application/xhtml+xml", "text/xml"} {
		if inlineImage[mime] {
			t.Errorf("%s står på listen over det, der vises inline", mime)
		}
	}
	// Og listen skal faktisk kunne noget, ellers virker billeder i noter ikke.
	for _, mime := range []string{"image/png", "image/jpeg"} {
		if !inlineImage[mime] {
			t.Errorf("%s mangler; billeder i noter kan ikke vises", mime)
		}
	}
}
