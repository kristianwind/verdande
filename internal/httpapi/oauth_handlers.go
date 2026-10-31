package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kristianwind/verdande/internal/store"
)

// Signing in to MCP with the account, rather than with a token pasted into a URL.
//
// Claude's connector dialog tries OAuth before anything else: it asks
// /.well-known for an authorization server, registers itself, sends the person
// to sign in and approve, and comes back with a code. Until this existed the
// answer was a 404, and the only way in was to make a token under Settings and
// paste it into the address — a secret copied between two programs, which is the
// step people stopped at.
//
// The shape is the smallest one the MCP authorization spec asks for:
//
//   - discovery: protected-resource metadata (RFC 9728) pointing at this instance
//     as its own authorization server (RFC 8414);
//   - dynamic client registration (RFC 7591), public clients only — a program
//     running somewhere else cannot keep a secret, so PKCE is the proof instead;
//   - the authorization-code grant with S256 PKCE, and nothing else.
//
// What an approval hands out is an ordinary personal API token. That is the
// whole permission model: a token is a person, it is listed under Settings →
// Tokens next to the hand-made ones, and revoking it there disconnects the
// client. No refresh token and no expiry, because there is nothing for one to
// protect that the token page does not already — a client that is cut off is
// answered 401 and asks the person to sign in again.

const oauthCodeTTL = time.Minute

// oauthGrant is what an authorization code stands for until it is exchanged.
type oauthGrant struct {
	userID      string
	clientID    string
	redirectURI string
	challenge   string
	scope       string
	expires     time.Time
}

// oauthCodes are held in memory, like the socket tickets: good for a minute and
// once. A restart in that minute costs one more click, which is cheaper than a
// table of short-lived secrets.
type oauthCodes struct {
	mu sync.Mutex
	m  map[string]oauthGrant
}

func newOAuthCodes() *oauthCodes { return &oauthCodes{m: map[string]oauthGrant{}} }

func (c *oauthCodes) issue(g oauthGrant) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := hex.EncodeToString(raw)

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	g.expires = now.Add(oauthCodeTTL)
	c.m[code] = g
	return code, nil
}

// redeem spends a code. Deleted before it is checked, so a code presented twice
// fails the second time whatever the first one did — a replayed code is the
// thing PKCE and single use are both there to stop.
func (c *oauthCodes) redeem(code string) (oauthGrant, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g, ok := c.m[code]
	delete(c.m, code)
	if !ok || time.Now().After(g.expires) {
		return oauthGrant{}, false
	}
	return g, true
}

// --- discovery -----------------------------------------------------------------------

// mcpResource is the address the protected resource is known by: the URL a
// person types into the connector dialog.
func (s *Server) mcpResource() string { return s.cfg.BaseURL + "/mcp" }

func (s *Server) protectedResourceMetadataURL() string {
	return s.cfg.BaseURL + "/.well-known/oauth-protected-resource/mcp"
}

func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.mcpResource(),
		"authorization_servers":    []string{s.cfg.BaseURL},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            s.cfg.ProductSlug(),
	})
}

func (s *Server) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	base := s.cfg.BaseURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	})
}

// openCORS lets a browser-based client read these answers. Safe for exactly the
// routes it is on: none of them reads a cookie, so a cross-site caller learns
// nothing and can do nothing it could not do with curl.
func openCORS(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
}

func handleOpenPreflight(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	w.WriteHeader(http.StatusNoContent)
}

// oauthError is the error shape RFC 6749 §5.2 prescribes for the token and
// registration endpoints — a client library parses `error`, not our `code`.
func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// --- registration --------------------------------------------------------------------

type registerRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	openCORS(w)

	// Not decodeJSON: a registration request carries whatever metadata the
	// client library fills in — logo_uri, grant_types, scope, contacts — and
	// RFC 7591 says a server ignores what it does not use. Refusing unknown
	// fields here would refuse every real client.
	var req registerRequest
	if err := decodeLenient(r, &req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body is not JSON")
		return
	}

	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri",
			"between one and ten redirect_uris are required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri",
				"redirect_uris must be https, http on a loopback address, or an app's own scheme: "+u)
			return
		}
	}
	if m := req.TokenEndpointAuthMethod; m != "" && m != "none" {
		// Said rather than silently downgraded: a client that believes it holds a
		// secret would send one and wonder why it was ignored.
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata",
			"only public clients are supported; use token_endpoint_auth_method \"none\" with PKCE")
		return
	}

	name := cleanClientName(req.ClientName)
	client, err := s.db.CreateOAuthClient(r.Context(), name, req.RedirectURIs)
	if err != nil {
		s.internal(w, r, "register oauth client", err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  client.ID,
		"client_id_issued_at":        client.CreatedAt.Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// cleanClientName makes the self-declared name fit to show. It is shown on the
// approval screen and on the tokens page, so it is trimmed, stripped of control
// characters and capped — and it is never trusted to say who the client is: the
// approval screen also shows where the code will be sent, which a client cannot
// choose to misstate.
func cleanClientName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if runes := []rune(name); len(runes) > 60 {
		name = strings.TrimSpace(string(runes[:60]))
	}
	if name == "" {
		name = "MCP-klient"
	}
	return name
}

// validRedirectURI is the rule for where a code may be sent.
//
// https anywhere; plain http only to this machine's loopback (RFC 8252 §7.3,
// where a desktop client listens on a port it picks at run time); and an app's
// own scheme (§7.1), which is how Cursor and its like receive one. The schemes a
// browser would *run* rather than navigate to are refused by name — the approval
// page hands the address to `location`, and `javascript:` there is script
// running on this origin.
func validRedirectURI(raw string) bool {
	if len(raw) > 2000 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.Scheme == "" {
		return false
	}
	switch scheme := strings.ToLower(u.Scheme); scheme {
	case "https":
		return u.Host != ""
	case "http":
		return isLoopback(u.Hostname())
	case "javascript", "data", "file", "vbscript", "blob", "about", "ws", "wss", "ftp":
		return false
	default:
		// The scheme's own syntax, RFC 3986 §3.1. url.Parse is lenient about it.
		for i, r := range scheme {
			if !(r >= 'a' && r <= 'z' || i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
				return false
			}
		}
		return true
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// redirectAllowed reports whether got is one of the client's registered
// addresses. Exact, except that a loopback address may differ in its port: a
// desktop client registers once and listens wherever the OS lets it next time.
func redirectAllowed(registered []string, got string) bool {
	for _, want := range registered {
		if want == got {
			return true
		}
	}
	g, err := url.Parse(got)
	if err != nil || g.Scheme != "http" || !isLoopback(g.Hostname()) {
		return false
	}
	for _, want := range registered {
		w, err := url.Parse(want)
		if err != nil || w.Scheme != "http" || !isLoopback(w.Hostname()) {
			continue
		}
		if w.Hostname() == g.Hostname() && w.Path == g.Path && w.RawQuery == g.RawQuery {
			return true
		}
	}
	return false
}

// --- authorization -------------------------------------------------------------------

// authorizeRequest is a checked /oauth/authorize query.
type authorizeRequest struct {
	client      *store.OAuthClient
	redirectURI string
	state       string
	challenge   string
	scope       string
}

// errBadClient means the client or its redirect address could not be trusted,
// so the answer must NOT be sent to that address (RFC 6749 §4.1.2.1) — it is
// shown to the person instead. Every other problem goes back to the client.
var errBadClient = errors.New("bad client")

// checkAuthorize validates a query. It runs twice — when the browser arrives,
// and again when the person presses approve — because the approval page is a
// page in the app, and what it posts back is whatever is in its address bar.
func (s *Server) checkAuthorize(r *http.Request, q url.Values) (*authorizeRequest, string, error) {
	client, err := s.db.OAuthClient(r.Context(), q.Get("client_id"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, "this app is not registered here — remove the connection and add it again", errBadClient
		}
		return nil, "", err
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(client.RedirectURIs) == 1 {
		redirect = client.RedirectURIs[0]
	}
	if !redirectAllowed(client.RedirectURIs, redirect) {
		return nil, "the app asked to be answered at an address it did not register", errBadClient
	}

	req := &authorizeRequest{
		client: client, redirectURI: redirect,
		state: q.Get("state"), challenge: q.Get("code_challenge"), scope: q.Get("scope"),
	}
	switch {
	case q.Get("response_type") != "code":
		return req, "unsupported_response_type", nil
	case req.challenge == "" || q.Get("code_challenge_method") != "S256":
		// OAuth 2.1 makes PKCE mandatory, and plain PKCE protects nothing that
		// S256 does not, so it is S256 or no code.
		return req, "invalid_request", nil
	}
	return req, "", nil
}

// redirectWith builds the address the browser is sent back to. Callers include
// `iss` (RFC 9207): it says who answered, so a client talking to several servers
// cannot be handed one server's code by another.
func (a *authorizeRequest) redirectWith(params url.Values) string {
	if a.state != "" {
		params.Set("state", a.state)
	}
	sep := "?"
	if strings.Contains(a.redirectURI, "?") {
		sep = "&"
	}
	return a.redirectURI + sep + params.Encode()
}

// handleOAuthAuthorize is where the client sends the browser. It checks the
// request and hands over to the app's approval page, which signs the person in
// first if it needs to — the same sign-in screen, passkeys and second factor
// included, rather than a second, lesser one written for OAuth.
func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req, problem, err := s.checkAuthorize(r, q)
	if errors.Is(err, errBadClient) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(s.cfg.ProductSlug() + ": " + problem + ".\n"))
		return
	}
	if err != nil {
		s.internal(w, r, "oauth authorize", err)
		return
	}
	if problem != "" {
		http.Redirect(w, r, req.redirectWith(url.Values{
			"error": {problem}, "iss": {s.cfg.BaseURL},
		}), http.StatusFound)
		return
	}
	http.Redirect(w, r, "/forbind?"+r.URL.RawQuery, http.StatusFound)
}

// handleOAuthRequest tells the approval page what it is approving.
func (s *Server) handleOAuthRequest(w http.ResponseWriter, r *http.Request) {
	req, problem, err := s.checkAuthorize(r, r.URL.Query())
	if err != nil && !errors.Is(err, errBadClient) {
		s.internal(w, r, "oauth request", err)
		return
	}
	if err == nil && problem != "" {
		problem = "the app's request is not valid (" + problem + ")"
	}
	if problem != "" {
		writeError(w, http.StatusBadRequest, CodeBadRequest, problem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"client_name": req.client.Name,
		// Where the code goes, which is the part a client cannot lie about. Any
		// program can register itself as "Claude"; only Claude is answered at
		// claude.ai.
		"redirect_to": redirectLabel(req.redirectURI),
	})
}

func redirectLabel(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Host != "" {
		return u.Hostname()
	}
	return u.Scheme + ":"
}

type approveRequest struct {
	// Query is the approval page's own query string, exactly as the client sent
	// it. Posted whole rather than as fields, so this and handleOAuthAuthorize
	// read one thing with one function and cannot drift apart.
	Query   string `json:"query"`
	Approve bool   `json:"approve"`
}

// handleOAuthApprove is the person's answer. Behind requireSession: an approval
// mints a token, and a token must not be able to mint another.
func (s *Server) handleOAuthApprove(w http.ResponseWriter, r *http.Request) {
	var body approveRequest
	if err := decodeJSON(w, r, &body); err != nil {
		return
	}
	q, err := url.ParseQuery(strings.TrimPrefix(body.Query, "?"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "the request could not be read")
		return
	}
	req, problem, err := s.checkAuthorize(r, q)
	if err != nil && !errors.Is(err, errBadClient) {
		s.internal(w, r, "oauth approve", err)
		return
	}
	if err == nil && problem != "" {
		problem = "the app's request is not valid (" + problem + ")"
	}
	if problem != "" {
		writeError(w, http.StatusBadRequest, CodeBadRequest, problem)
		return
	}

	if !body.Approve {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_to": req.redirectWith(url.Values{
			"error": {"access_denied"}, "iss": {s.cfg.BaseURL},
		})})
		return
	}

	code, err := s.oauthCodes.issue(oauthGrant{
		userID: userFrom(r.Context()).ID, clientID: req.client.ID,
		redirectURI: req.redirectURI, challenge: req.challenge, scope: req.scope,
	})
	if err != nil {
		s.internal(w, r, "oauth code", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_to": req.redirectWith(url.Values{
		"code": {code}, "iss": {s.cfg.BaseURL},
	})})
}

// --- token ---------------------------------------------------------------------------

func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	openCORS(w)
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "the body must be form-encoded")
		return
	}
	if gt := r.PostForm.Get("grant_type"); gt != "authorization_code" {
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type",
			"only authorization_code is supported")
		return
	}

	clientID := r.PostForm.Get("client_id")
	if id, _, ok := r.BasicAuth(); ok && clientID == "" {
		clientID = id
	}

	grant, ok := s.oauthCodes.redeem(r.PostForm.Get("code"))
	if !ok {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is unknown, used or expired")
		return
	}
	// Each of these is a code presented by somebody other than the one it was
	// issued to, or for somewhere else. The code is already spent either way.
	if clientID != grant.clientID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code was issued to another client")
		return
	}
	if ru := r.PostForm.Get("redirect_uri"); ru != "" && ru != grant.redirectURI {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization")
		return
	}
	if !pkceMatches(r.PostForm.Get("code_verifier"), grant.challenge) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "code_verifier does not match")
		return
	}

	client, err := s.db.OAuthClient(r.Context(), grant.clientID)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client", "the client is no longer registered")
		return
	}
	token, err := s.db.CreateOAuthToken(r.Context(), grant.userID, client)
	if err != nil {
		s.internal(w, r, "oauth token", err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	out := map[string]string{"access_token": token, "token_type": "Bearer"}
	if grant.scope != "" {
		out["scope"] = grant.scope
	}
	writeJSON(w, http.StatusOK, out)
}

// pkceMatches is RFC 7636 §4.6 for S256: the challenge is the unpadded base64url
// SHA-256 of the verifier.
func pkceMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

// decodeLenient reads a JSON body that may carry fields this server does not
// use. Only for the registration endpoint — see the comment there.
func decodeLenient(r *http.Request, dst any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(dst)
}
