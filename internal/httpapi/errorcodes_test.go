package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The comment above the code list in errors.go says they are kept in one place
// "so the frontend's mapping can be checked against it rather than discovered
// from whatever a handler happened to write". Nothing checked it, and the
// frontend was quietly missing entries — which is not a cosmetic gap: a code
// with no message falls through to the server's English log prose, and a code
// mapped to the wrong sentence tells the person something untrue. Connecting
// Gmail without an OAuth client said "Det er der allerede."

var (
	codeConst   = regexp.MustCompile(`(?m)^\s*Code\w+\s*=\s*"([a-z_]+)"`)
	messageKey  = regexp.MustCompile(`(?m)^\s*([a-z_]+):\s*$|^\s*([a-z_]+):\s*'`)
	messagesTop = "const MESSAGES = {"
)

func TestEveryErrorCodeHasAMessageInTheFrontend(t *testing.T) {
	codes := serverErrorCodes(t)
	messages := frontendMessageKeys(t)

	var missing []string
	for _, code := range codes {
		if !messages[code] {
			missing = append(missing, code)
		}
	}
	sort.Strings(missing)

	for _, code := range missing {
		t.Errorf("%q is returned by the API and has no message in web/src/lib/api.js — "+
			"a person would be shown the server's English log prose", code)
	}
}

func serverErrorCodes(t *testing.T) []string {
	t.Helper()

	source, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatalf("read errors.go: %v", err)
	}
	var codes []string
	for _, m := range codeConst.FindAllStringSubmatch(string(source), -1) {
		codes = append(codes, m[1])
	}
	if len(codes) == 0 {
		t.Fatal("no codes were read out of errors.go — the scanner and the file disagree")
	}
	return codes
}

// frontendMessageKeys reads the keys of the MESSAGES object. A small scanner
// rather than a JS parser, for the same reason the OpenAPI check uses one: it
// needs one thing out of a file this test also owns.
func frontendMessageKeys(t *testing.T) map[string]bool {
	t.Helper()

	source, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "api.js"))
	if err != nil {
		t.Fatalf("read api.js: %v", err)
	}
	body := string(source)

	start := strings.Index(body, messagesTop)
	if start < 0 {
		t.Fatalf("could not find %q in api.js", messagesTop)
	}
	body = body[start+len(messagesTop):]
	end := strings.Index(body, "\n};")
	if end < 0 {
		t.Fatal("the MESSAGES object does not end where this scanner expects")
	}

	keys := map[string]bool{}
	for _, line := range strings.Split(body[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		name, _, ok := strings.Cut(line, ":")
		if ok {
			keys[strings.TrimSpace(name)] = true
		}
	}
	if len(keys) == 0 {
		t.Fatal("no message keys were read out of api.js")
	}
	return keys
}

// The four that used to be indistinguishable from "that already exists".
func TestTheMisleadingConflictsHaveTheirOwnCodes(t *testing.T) {
	for _, code := range []string{
		CodeGmailNotConfigured,
		CodeAINotConfigured,
		CodeTOTPNotEnabled,
		CodeInboxProtected,
	} {
		if code == CodeConflict {
			t.Errorf("%q is still plain conflict", code)
		}
	}

	messages := frontendMessageKeys(t)
	for _, code := range []string{"gmail_not_configured", "ai_not_configured",
		"totp_not_enabled", "inbox_protected"} {
		if !messages[code] {
			t.Errorf("%q has no Danish message", code)
		}
	}
}

// The Inbox is the one project that cannot be deleted, and saying "that already
// exists" about it helps nobody.
func TestDeletingTheInboxSaysWhy(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, projects := ts.do(t, "GET", "/api/v1/projects", nil)
	list, _ := projects["projects"].([]any)

	var inboxID string
	for _, raw := range list {
		p := raw.(map[string]any)
		if p["is_inbox"] == true {
			inboxID = p["id"].(string)
		}
	}
	if inboxID == "" {
		t.Fatal("no inbox was found")
	}

	resp, body := ts.do(t, "DELETE", "/api/v1/projects/"+inboxID, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("status %d, want 409", resp.StatusCode)
	}
	if body["code"] != CodeInboxProtected {
		t.Errorf("code = %v, want %q", body["code"], CodeInboxProtected)
	}
}
