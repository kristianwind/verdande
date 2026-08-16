package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"regexp"
	"strings"
)

// inlineScript matches a <script> block that has no src attribute — that is, one
// whose body is executed inline and therefore needs to be named in the CSP.
var inlineScript = regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`)

// hasSrc distinguishes <script src="..."> from an inline block. Only the latter
// needs a hash; the former is already covered by 'self'.
var hasSrc = regexp.MustCompile(`(?is)^<script[^>]*\ssrc\s*=`)

// scriptHashes returns the CSP source expressions for every inline script in the
// built index.html.
//
// This is computed at startup from the shipped file rather than written down as a
// constant, because both inline scripts change: SvelteKit's bootstrap is rebuilt on
// every frontend build, and the theme script in app.html changes whenever it is
// edited. A hard-coded hash would go stale silently and the symptom would be a
// blank page in production only — the CSP is not enforced when the page is opened
// from a file, and a stale hash never fails a test.
//
// The alternative, 'unsafe-inline', would drop the one protection that makes an
// injected <script> in a task title inert. Two hashes are cheaper than that.
func scriptHashes(web fs.FS) []string {
	if web == nil {
		return nil
	}
	raw, err := fs.ReadFile(web, "index.html")
	if err != nil {
		return nil
	}

	var out []string
	seen := map[string]bool{}
	for _, match := range inlineScript.FindAllStringSubmatchIndex(string(raw), -1) {
		tag := string(raw[match[0]:match[1]])
		if hasSrc.MatchString(tag) {
			continue
		}
		body := string(raw[match[2]:match[3]])
		if strings.TrimSpace(body) == "" {
			continue
		}
		// The hash is over the script's exact contents, byte for byte — including
		// leading and trailing whitespace, which is why the body is not trimmed.
		sum := sha256.Sum256([]byte(body))
		expr := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !seen[expr] {
			seen[expr] = true
			out = append(out, expr)
		}
	}
	return out
}

// contentSecurityPolicy assembles the policy served with every response.
//
// It is strict because verdande loads nothing from anywhere else: no CDN, no
// analytics, no webfont. The one concession is style-src 'unsafe-inline', which
// Svelte's scoped styles and the inline `style=` on an avatar colour both need;
// inline CSS cannot execute, so it is a far smaller allowance than the script
// equivalent.
func contentSecurityPolicy(hashes []string) string {
	scriptSrc := append([]string{"'self'"}, hashes...)

	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scriptSrc, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self'",
		// The WebSocket is same-origin, so 'self' covers ws: and wss: to this host.
		"connect-src 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	}, "; ")
}
