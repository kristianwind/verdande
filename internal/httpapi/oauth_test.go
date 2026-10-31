package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Signing in to MCP with the account: the whole dance Claude performs, from the
// first 401 to a tool call made with the token it was handed.

const claudeCallback = "https://claude.ai/api/mcp/auth_callback"

// A verifier of the minimum legal length, and its S256 challenge.
var (
	testVerifier  = strings.Repeat("v", 43)
	testChallenge = func() string {
		sum := sha256.Sum256([]byte(testVerifier))
		return base64.RawURLEncoding.EncodeToString(sum[:])
	}()
)

func postJSON(t *testing.T, target, body string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.Post(target, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

// registerClient registers the way Claude does, extra metadata included.
func registerClient(t *testing.T, ts *testServer, redirect string) string {
	t.Helper()
	resp, body := postJSON(t, ts.URL+"/oauth/register", `{
		"client_name": "Claude",
		"redirect_uris": ["`+redirect+`"],
		"grant_types": ["authorization_code", "refresh_token"],
		"response_types": ["code"],
		"token_endpoint_auth_method": "none",
		"scope": "claudeai"
	}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: status %d, %v", resp.StatusCode, body)
	}
	id, _ := body["client_id"].(string)
	if id == "" {
		t.Fatalf("register: no client_id in %v", body)
	}
	return id
}

func authorizeQuery(clientID, redirect string) string {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"state":                 {"st4te"},
		"code_challenge":        {testChallenge},
		"code_challenge_method": {"S256"},
		"resource":              {"http://localhost/mcp"},
	}.Encode()
}

// approve is the approval page pressing "Tillad", as the signed-in ts.client.
// It returns the code from the address the browser is sent back to.
func approve(t *testing.T, ts *testServer, query string) string {
	t.Helper()
	resp, body := ts.do(t, "POST", "/api/v1/oauth/approve", map[string]any{
		"query": "?" + query, "approve": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve: status %d, %v", resp.StatusCode, body)
	}
	back, err := url.Parse(body["redirect_to"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Query().Get("state"); got != "st4te" {
		t.Errorf("state = %q, want it handed back unchanged", got)
	}
	return back.Query().Get("code")
}

func exchange(t *testing.T, ts *testServer, form url.Values) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(ts.URL+"/oauth/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp, decoded
}

func codeForm(clientID, code string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {claudeCallback}, "code_verifier": {testVerifier},
	}
}

func mcpInitialize(t *testing.T, ts *testServer, bearer string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestMCPSignInWithTheAccount(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// 1. The connector's first request has no credential, and the answer says
	//    where to read how to get one.
	resp := mcpInitialize(t, ts, "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /mcp: status %d", resp.StatusCode)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	want := `resource_metadata="http://localhost/.well-known/oauth-protected-resource/mcp"`
	if !strings.Contains(challenge, want) {
		t.Fatalf("WWW-Authenticate = %q, want it to contain %s", challenge, want)
	}

	// 2. Discovery, both documents.
	var prm, asm map[string]any
	for path, into := range map[string]*map[string]any{
		"/.well-known/oauth-protected-resource/mcp": &prm,
		"/.well-known/oauth-authorization-server":   &asm,
	} {
		r, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		json.NewDecoder(r.Body).Decode(into)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d", path, r.StatusCode)
		}
	}
	if prm["resource"] != "http://localhost/mcp" {
		t.Errorf("resource = %v, want the address a person types in", prm["resource"])
	}
	if asm["issuer"] != "http://localhost" || asm["registration_endpoint"] != "http://localhost/oauth/register" {
		t.Errorf("authorization server metadata = %v", asm)
	}

	// 3. Registration, 4. the browser's visit, 5. approval, 6. the exchange.
	clientID := registerClient(t, ts, claudeCallback)
	query := authorizeQuery(clientID, claudeCallback)

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	r, err := noFollow.Get(ts.URL + "/oauth/authorize?" + query)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if loc := r.Header.Get("Location"); r.StatusCode != http.StatusFound || !strings.HasPrefix(loc, "/forbind?") {
		t.Fatalf("authorize: status %d, Location %q — want the approval page", r.StatusCode, loc)
	}

	// The approval page's question: what is this, and where does it go.
	info, body := ts.do(t, "GET", "/api/v1/oauth/request?"+query, nil)
	if info.StatusCode != http.StatusOK || body["client_name"] != "Claude" || body["redirect_to"] != "claude.ai" {
		t.Fatalf("request info: status %d, %v", info.StatusCode, body)
	}

	code := approve(t, ts, query)
	tokResp, tok := exchange(t, ts, codeForm(clientID, code))
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("token: status %d, %v", tokResp.StatusCode, tok)
	}
	access, _ := tok["access_token"].(string)
	if tok["token_type"] != "Bearer" || !strings.HasPrefix(access, "vrd_") {
		t.Fatalf("token response = %v", tok)
	}
	if cc := tokResp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q on a response carrying a token", cc)
	}

	// 7. The token works where the person typed the address.
	if resp := mcpInitialize(t, ts, access); resp.StatusCode != http.StatusOK {
		t.Fatalf("/mcp with the token: status %d", resp.StatusCode)
	}

	// 8. And it is on the tokens page, under the client's name, marked as such.
	_, list := ts.do(t, "GET", "/api/v1/tokens", nil)
	tokens, _ := list["tokens"].([]any)
	if len(tokens) != 1 {
		t.Fatalf("tokens page lists %d, want 1: %v", len(tokens), list)
	}
	entry := tokens[0].(map[string]any)
	if entry["name"] != "Claude" || entry["connected"] != true {
		t.Errorf("token entry = %v", entry)
	}

	// 9. Revoked there, the client is told to sign in again.
	if resp, _ := ts.do(t, "DELETE", "/api/v1/tokens/"+entry["id"].(string), nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: status %d", resp.StatusCode)
	}
	resp = mcpInitialize(t, ts, access)
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("revoked token: status %d, WWW-Authenticate %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

// A code is good once. A second exchange — a replay, or a code that leaked — gets
// nothing, and neither does a wrong verifier, the wrong client or the wrong
// redirect: each is somebody other than the one the code was issued to.
func TestOAuthCodeIsBoundAndSingleUse(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	clientID := registerClient(t, ts, claudeCallback)
	other := registerClient(t, ts, claudeCallback)
	query := authorizeQuery(clientID, claudeCallback)

	cases := map[string]func(url.Values){
		"wrong verifier": func(f url.Values) { f.Set("code_verifier", strings.Repeat("w", 43)) },
		"no verifier":    func(f url.Values) { f.Del("code_verifier") },
		"other client":   func(f url.Values) { f.Set("client_id", other) },
		"other redirect": func(f url.Values) { f.Set("redirect_uri", "https://claude.ai/elsewhere") },
	}
	for name, spoil := range cases {
		code := approve(t, ts, query)
		form := codeForm(clientID, code)
		spoil(form)
		if resp, body := exchange(t, ts, form); resp.StatusCode != http.StatusBadRequest || body["error"] != "invalid_grant" {
			t.Errorf("%s: status %d, %v — want invalid_grant", name, resp.StatusCode, body)
		}
		// Spent by the failed attempt, so the rightful owner cannot use it either.
		if resp, _ := exchange(t, ts, codeForm(clientID, code)); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: the code survived a failed exchange", name)
		}
	}

	code := approve(t, ts, query)
	if resp, _ := exchange(t, ts, codeForm(clientID, code)); resp.StatusCode != http.StatusOK {
		t.Fatalf("first exchange: status %d", resp.StatusCode)
	}
	if resp, body := exchange(t, ts, codeForm(clientID, code)); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("replayed code: status %d, %v", resp.StatusCode, body)
	}
}

// Where a code may be sent is the whole security of the approval: a client
// registers its addresses, and a request for any other is refused without
// sending anything anywhere.
func TestOAuthRedirectRules(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, bad := range []string{
		"javascript:alert(1)",
		"data:text/html,hi",
		"http://evil.example/cb", // plain http off this machine
		"https://claude.ai/cb#frag",
		"not a uri",
	} {
		resp, body := postJSON(t, ts.URL+"/oauth/register",
			`{"client_name":"x","redirect_uris":[`+jsonString(bad)+`]}`)
		if resp.StatusCode != http.StatusBadRequest || body["error"] != "invalid_redirect_uri" {
			t.Errorf("registering %q: status %d, %v", bad, resp.StatusCode, body)
		}
	}

	clientID := registerClient(t, ts, claudeCallback)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	r, err := noFollow.Get(ts.URL + "/oauth/authorize?" + authorizeQuery(clientID, "https://evil.example/cb"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Errorf("unregistered redirect: status %d, Location %q — want a refusal shown here",
			r.StatusCode, r.Header.Get("Location"))
	}
	resp, _ := ts.do(t, "POST", "/api/v1/oauth/approve", map[string]any{
		"query": authorizeQuery(clientID, "https://evil.example/cb"), "approve": true,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("approving an unregistered redirect: status %d", resp.StatusCode)
	}

	// A desktop client listens on whatever port it is given that day.
	loopback := registerClient(t, ts, "http://127.0.0.1/callback")
	r, err = noFollow.Get(ts.URL + "/oauth/authorize?" + authorizeQuery(loopback, "http://127.0.0.1:53682/callback"))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/forbind?") {
		t.Errorf("loopback with a port: status %d, Location %q", r.StatusCode, r.Header.Get("Location"))
	}

	// No PKCE is answered to the client — its address is trusted by now.
	q, _ := url.ParseQuery(authorizeQuery(clientID, claudeCallback))
	q.Del("code_challenge")
	r, err = noFollow.Get(ts.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if loc := r.Header.Get("Location"); !strings.HasPrefix(loc, claudeCallback+"?") || !strings.Contains(loc, "error=invalid_request") {
		t.Errorf("no PKCE: Location %q", loc)
	}
}

// An approval mints a token, so a token must not be able to approve: otherwise
// one leaked token is a machine for making more that survive its revocation.
func TestOAuthApprovalNeedsASession(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	var userID string
	if err := ts.db.QueryRowContext(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	key := ts.apiToken(t, userID)
	clientID := registerClient(t, ts, claudeCallback)

	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/oauth/approve",
		strings.NewReader(`{"query":"`+authorizeQuery(clientID, claudeCallback)+`","approve":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("approval with an API token: status %d, want 403", resp.StatusCode)
	}
}

// "Afvis" sends the client away with access_denied and no code.
func TestOAuthDenial(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	clientID := registerClient(t, ts, claudeCallback)

	_, body := ts.do(t, "POST", "/api/v1/oauth/approve", map[string]any{
		"query": authorizeQuery(clientID, claudeCallback), "approve": false,
	})
	back, _ := url.Parse(body["redirect_to"].(string))
	if back.Query().Get("error") != "access_denied" || back.Query().Get("code") != "" {
		t.Errorf("denial redirect = %v", body["redirect_to"])
	}
}

// Connecting the same client again replaces its token rather than adding one.
func TestOAuthReconnectReplacesTheToken(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	clientID := registerClient(t, ts, claudeCallback)
	query := authorizeQuery(clientID, claudeCallback)

	_, first := exchange(t, ts, codeForm(clientID, approve(t, ts, query)))
	_, second := exchange(t, ts, codeForm(clientID, approve(t, ts, query)))

	_, list := ts.do(t, "GET", "/api/v1/tokens", nil)
	if n := len(list["tokens"].([]any)); n != 1 {
		t.Errorf("tokens after reconnecting: %d, want 1", n)
	}
	if resp := mcpInitialize(t, ts, first["access_token"].(string)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the replaced token still works: status %d", resp.StatusCode)
	}
	if resp := mcpInitialize(t, ts, second["access_token"].(string)); resp.StatusCode != http.StatusOK {
		t.Errorf("the new token does not work: status %d", resp.StatusCode)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
