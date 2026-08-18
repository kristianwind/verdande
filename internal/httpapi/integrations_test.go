package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kristianwind/verdande/internal/config"
)

// --- MCP -----------------------------------------------------------------------------

// rpc sends one JSON-RPC message to the MCP endpoint.
func (ts *testServer) rpc(t *testing.T, method string, params any) map[string]any {
	t.Helper()

	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	_, decoded := ts.do(t, "POST", "/api/v1/mcp", body)
	return decoded
}

func TestMCPHandshake(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	init := ts.rpc(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "claude", "version": "1"},
	})
	result, _ := init["result"].(map[string]any)
	if result == nil {
		t.Fatalf("initialize returned no result: %v", init)
	}
	if result["protocolVersion"] == nil {
		t.Error("no protocol version was announced")
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("tools were not announced as a capability: %v", caps)
	}

	listed := ts.rpc(t, "tools/list", nil)
	result, _ = listed["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("no tools were listed")
	}

	// The tools the brief names have to be there, each with a schema a model can
	// actually call from.
	names := map[string]bool{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		names[name] = true

		if tool["description"] == "" {
			t.Errorf("tool %q has no description", name)
		}
		if _, ok := tool["inputSchema"].(map[string]any); !ok {
			t.Errorf("tool %q has no input schema", name)
		}
	}
	for _, want := range []string{
		"search_tasks", "create_task", "update_task", "complete_task",
		"list_projects", "add_comment",
	} {
		if !names[want] {
			t.Errorf("the %q tool is missing", want)
		}
	}
}

// A notification has no id, and the spec says to answer with nothing at all.
func TestMCPNotificationGetsNoResponse(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "POST", "/api/v1/mcp", map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized",
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("a notification got status %d, want 202 with no body", resp.StatusCode)
	}
}

func TestMCPToolsDoTheWork(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// create_task, through the natural-language path a model is told to prefer.
	created := ts.callTool(t, "create_task", map[string]any{
		"text": "betal moms i morgen kl 10 p1",
	})
	if created["id"] == nil {
		t.Fatalf("create_task returned no id: %v", created)
	}
	if created["priority"] != float64(1) {
		t.Errorf("priority = %v, want 1 — the text was not parsed", created["priority"])
	}
	taskID := created["id"].(string)

	// search_tasks, with the filter language.
	found := ts.callTool(t, "search_tasks", map[string]any{"query": "p1"})
	tasks, _ := found["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("search_tasks found %d tasks, want 1", len(tasks))
	}

	// list_projects
	projects := ts.callTool(t, "list_projects", map[string]any{})
	if got, _ := projects["projects"].([]any); len(got) == 0 {
		t.Error("list_projects returned nothing")
	}

	// update_task
	updated := ts.callTool(t, "update_task", map[string]any{
		"task_id": taskID, "priority": 3,
	})
	if updated["priority"] != float64(3) {
		t.Errorf("priority = %v after update, want 3", updated["priority"])
	}

	// add_comment
	comment := ts.callTool(t, "add_comment", map[string]any{
		"task_id": taskID, "body": "sagt af Claude",
	})
	if comment["id"] == nil {
		t.Errorf("add_comment returned no id: %v", comment)
	}

	// complete_task
	done := ts.callTool(t, "complete_task", map[string]any{"task_id": taskID})
	if done["completed"] != true {
		t.Errorf("complete_task did not complete: %v", done)
	}
}

// A tool called wrongly must produce a readable tool error, not a protocol error —
// a model can recover from the first and can do nothing with the second.
func TestMCPToolErrorsAreReadable(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	response := ts.rpc(t, "tools/call", map[string]any{
		"name":      "complete_task",
		"arguments": map[string]any{"task_id": "findes-ikke"},
	})

	if response["error"] != nil {
		t.Errorf("a failed tool call produced a JSON-RPC error rather than a tool result: %v", response["error"])
	}
	result, _ := response["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("the result is not marked as an error: %v", result)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("the error has no content for the model to read")
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "findes-ikke") {
		t.Errorf("the message does not say what was wrong: %q", text)
	}
}

// An MCP connector reaches exactly what its owner reaches, and nothing else.
func TestMCPCannotReachAnotherUsersTasks(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "min private opgave"})

	found := other.callTool(t, "search_tasks", map[string]any{"text": "private"})
	if tasks, _ := found["tasks"].([]any); len(tasks) != 0 {
		t.Errorf("another user's MCP session found %d of the owner's tasks", len(tasks))
	}
}

// callTool runs one tool and returns its decoded result.
func (ts *testServer) callTool(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	response := ts.rpc(t, "tools/call", map[string]any{"name": name, "arguments": args})
	if response["error"] != nil {
		t.Fatalf("%s: protocol error %v", name, response["error"])
	}
	result, _ := response["result"].(map[string]any)
	if result == nil {
		t.Fatalf("%s: no result", name)
	}
	if result["isError"] == true {
		content, _ := result["content"].([]any)
		text := ""
		if len(content) > 0 {
			text, _ = content[0].(map[string]any)["text"].(string)
		}
		t.Fatalf("%s failed: %s", name, text)
	}

	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("%s: no content", name)
	}
	text, _ := content[0].(map[string]any)["text"].(string)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		t.Fatalf("%s: the result is not JSON: %v\n%s", name, err, text)
	}
	return decoded
}

// --- CalDAV ---------------------------------------------------------------------------

// dav sends a WebDAV request with Basic auth, as a real client would.
func (ts *testServer) dav(t *testing.T, method, path, token, email, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(email, token)
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// apiToken mints a personal token, which is what CalDAV clients authenticate with.
func (ts *testServer) apiToken(t *testing.T, userID string) string {
	t.Helper()
	token, _, err := ts.db.CreateAPIToken(t.Context(), userID, "caldav", nil)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestCalDAVRequiresCredentials(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, err := (&http.Client{}).Get(ts.URL + "/caldav/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", resp.StatusCode)
	}
	// Without a challenge, a client has nothing to prompt with.
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("no WWW-Authenticate header was sent")
	}
}

// The account password must not work here — a client stores what it is given, and
// what it is given should not be able to sign in to the app.
func TestCalDAVRejectsTheAccountPassword(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp := ts.dav(t, "PROPFIND", "/caldav/", "syv heste over marken", "kristian@example.dk", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the account password was accepted: status %d", resp.StatusCode)
	}
}

func TestCalDAVDiscovery(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, user.ID)

	// OPTIONS is what a client reads first. Without calendar-access in the DAV
	// header, Apple Reminders decides this is plain WebDAV and stops.
	options := ts.dav(t, "OPTIONS", "/caldav/", token, user.Email, "")
	if dav := options.Header.Get("DAV"); !strings.Contains(dav, "calendar-access") {
		t.Errorf("DAV header = %q, want calendar-access in it", dav)
	}

	principal := ts.dav(t, "PROPFIND", "/caldav/", token, user.Email, "")
	if principal.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND on the root: status %d, want 207", principal.StatusCode)
	}
	body, _ := io.ReadAll(principal.Body)
	if !strings.Contains(string(body), "calendar-home-set") {
		t.Errorf("the principal response does not point at a calendar home:\n%s", body)
	}

	// The home lists one collection per project.
	home := ts.dav(t, "PROPFIND", "/caldav/"+user.ID+"/", token, user.Email, "")
	if home.StatusCode != http.StatusMultiStatus {
		t.Fatalf("PROPFIND on the home: status %d", home.StatusCode)
	}
	homeBody, _ := io.ReadAll(home.Body)
	if !strings.Contains(string(homeBody), "<cal:comp name=\"VTODO\"/>") {
		t.Errorf("the collections do not declare VTODO support:\n%s", homeBody)
	}
	if !strings.Contains(string(homeBody), "getctag") {
		t.Error("no ctag was returned; clients would re-sync everything every time")
	}
}

// The half that makes CalDAV two-way: a client creating and completing a task.
func TestCalDAVReadWrite(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, user.ID)
	inbox, err := ts.db.InboxID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}

	// A client PUTs a VTODO it made up an id for.
	vtodo := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VTODO\r\n" +
		"UID:fra-reminders\r\nSUMMARY:Købt af Apple Reminders\r\n" +
		"DUE;VALUE=DATE:20260315\r\nPRIORITY:1\r\nSTATUS:NEEDS-ACTION\r\n" +
		"END:VTODO\r\nEND:VCALENDAR\r\n"

	put := ts.dav(t, "PUT", "/caldav/"+user.ID+"/"+inbox+"/fra-reminders.ics",
		token, user.Email, vtodo)
	if put.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(put.Body)
		t.Fatalf("PUT: status %d, %s", put.StatusCode, body)
	}

	// It is a real task now.
	task, err := ts.db.GetTask(t.Context(), "fra-reminders", user.ID)
	if err != nil {
		t.Fatalf("the task was not created: %v", err)
	}
	if task.Content != "Købt af Apple Reminders" {
		t.Errorf("content = %q", task.Content)
	}
	if task.DueDate != "2026-03-15" {
		t.Errorf("due_date = %q", task.DueDate)
	}
	if task.Priority != 1 {
		t.Errorf("priority = %d, want 1 (iCalendar's 1)", task.Priority)
	}

	// And it reads back as a VTODO.
	get := ts.dav(t, "GET", "/caldav/"+user.ID+"/"+inbox+"/fra-reminders.ics", token, user.Email, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("GET: status %d", get.StatusCode)
	}
	body, _ := io.ReadAll(get.Body)
	if !strings.Contains(string(body), "BEGIN:VTODO") {
		t.Errorf("the response is not a VTODO:\n%s", body)
	}
	if !strings.Contains(string(body), "SUMMARY:Købt af Apple Reminders") {
		t.Errorf("the summary is missing:\n%s", body)
	}

	// Ticking it off in the client arrives as a PUT with STATUS:COMPLETED.
	completed := strings.Replace(vtodo, "STATUS:NEEDS-ACTION", "STATUS:COMPLETED", 1)
	done := ts.dav(t, "PUT", "/caldav/"+user.ID+"/"+inbox+"/fra-reminders.ics",
		token, user.Email, completed)
	if done.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT to complete: status %d", done.StatusCode)
	}

	after, err := ts.db.GetTask(t.Context(), "fra-reminders", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Completed() {
		t.Error("completing the task in a CalDAV client did not complete it here")
	}

	// A task completed in the app must stay visible to the client, or the client
	// treats it as deleted and drops its own record of having done it.
	report := ts.dav(t, "REPORT", "/caldav/"+user.ID+"/"+inbox+"/", token, user.Email,
		`<?xml version="1.0"?><c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">`+
			`<d:prop><d:getetag/><c:calendar-data/></d:prop></c:calendar-query>`)
	if report.StatusCode != http.StatusMultiStatus {
		t.Fatalf("REPORT: status %d", report.StatusCode)
	}
	reportBody, _ := io.ReadAll(report.Body)
	if !strings.Contains(string(reportBody), "fra-reminders") {
		t.Errorf("a completed task vanished from the collection:\n%s", reportBody)
	}
}

// --- mail to task -------------------------------------------------------------------------

func TestMailToTask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]any{"name": "Firma"})
	projectID := project["id"].(string)

	resp, body := ts.do(t, "GET", "/api/v1/mail-address", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mail address: %d %v", resp.StatusCode, body)
	}
	address, _ := body["address"].(string)
	if !strings.HasPrefix(address, "todo+") {
		t.Fatalf("address = %q, want todo+<token>@domain", address)
	}

	// Delivered mail. The subject goes through the quick-add parser.
	resp, created := ts.do(t, "POST", "/inbound/mail", map[string]any{
		"to":      address,
		"from":    "revisor@example.dk",
		"subject": "Send årsregnskab p1 #Firma",
		"body":    "Vedhæftet er sidste års tal.",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("inbound mail: %d %v", resp.StatusCode, created)
	}

	_, list := ts.do(t, "GET", "/api/v1/tasks?project_id="+projectID, nil)
	tasks, _ := list["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks in #Firma, want the one from the mail", len(tasks))
	}
	task := tasks[0].(map[string]any)
	if task["content"] != "Send årsregnskab" {
		t.Errorf("content = %v", task["content"])
	}
	if task["priority"] != float64(1) {
		t.Errorf("priority = %v, want 1", task["priority"])
	}
	if task["description"] != "Vedhæftet er sidste års tal." {
		t.Errorf("the body did not become the description: %v", task["description"])
	}
}

// An invented address must not create anything, and must not say whether the token
// merely does not exist — that would be an oracle for which addresses are live.
func TestInboundMailToAnUnknownAddress(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "POST", "/inbound/mail", map[string]any{
		"to": "todo+opfundet@example.dk", "subject": "hej", "body": "",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}

	malformed, _ := ts.do(t, "POST", "/inbound/mail", map[string]any{
		"to": "ikke-en-adresse", "subject": "hej", "body": "",
	})
	if malformed.StatusCode != resp.StatusCode {
		t.Errorf("a malformed address answers %d and an unknown one %d; they must match",
			malformed.StatusCode, resp.StatusCode)
	}
}

func TestRotatingTheMailAddress(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, first := ts.do(t, "GET", "/api/v1/mail-address", nil)
	old := first["address"].(string)

	_, second := ts.do(t, "POST", "/api/v1/mail-address/rotate", nil)
	fresh := second["address"].(string)

	if old == fresh {
		t.Fatal("rotating produced the same address")
	}
	resp, _ := ts.do(t, "POST", "/inbound/mail", map[string]any{
		"to": old, "subject": "til den gamle adresse", "body": "",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("the old address still works: status %d", resp.StatusCode)
	}
}

// --- AI ---------------------------------------------------------------------------------

// With no provider configured the AI features are off, not broken. That is the
// promise: the app works without them.
func TestAIDegradesWhenNotConfigured(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "GET", "/api/v1/ai/settings", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ai settings: %d %v", resp.StatusCode, body)
	}
	if body["has_key"] != false {
		t.Errorf("has_key = %v on a fresh instance", body["has_key"])
	}

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "flyt"})
	resp, _ = ts.do(t, "POST", "/api/v1/ai/tasks/"+task["id"].(string)+"/split", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("split with no provider: status %d, want 409", resp.StatusCode)
	}

	resp, _ = ts.do(t, "POST", "/api/v1/ai/summary", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("summary with no provider: status %d, want 409", resp.StatusCode)
	}
}

// The API key is stored but never handed back — a settings page that repopulates a
// password field is one that will eventually leak it into a screenshot.
func TestAIKeyIsNeverReturned(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, _ := ts.do(t, "PUT", "/api/v1/ai/settings", map[string]any{
		"provider": "anthropic", "model": "claude-sonnet-5", "api_key": "sk-hemmelig-nøgle",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("save settings: status %d", resp.StatusCode)
	}

	_, body := ts.do(t, "GET", "/api/v1/ai/settings", nil)
	if body["has_key"] != true {
		t.Error("has_key is false after saving a key")
	}
	if body["api_key"] != nil && body["api_key"] != "" {
		t.Errorf("the key was returned: %v", body["api_key"])
	}
	if body["model"] != "claude-sonnet-5" {
		t.Errorf("model = %v", body["model"])
	}

	// Saving again without a key must keep the stored one, or changing the model
	// would silently disconnect the provider.
	ts.do(t, "PUT", "/api/v1/ai/settings", map[string]any{
		"provider": "anthropic", "model": "claude-opus-5",
	})
	_, after := ts.do(t, "GET", "/api/v1/ai/settings", nil)
	if after["has_key"] != true {
		t.Error("the stored key was cleared by a save that did not include one")
	}
	if after["model"] != "claude-opus-5" {
		t.Errorf("model = %v after update", after["model"])
	}
}

func TestGmailSettings(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "GET", "/api/v1/gmail", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gmail settings: %d %v", resp.StatusCode, body)
	}
	if body["connected"] != false {
		t.Error("a fresh instance reports Gmail as connected")
	}

	resp, _ = ts.do(t, "PUT", "/api/v1/gmail", map[string]any{
		"trigger": "starred", "label": "Til handling",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("save: status %d", resp.StatusCode)
	}
	_, after := ts.do(t, "GET", "/api/v1/gmail", nil)
	if after["trigger"] != "starred" || after["label"] != "Til handling" {
		t.Errorf("settings did not persist: %v", after)
	}
}

// --- Gmail OAuth ------------------------------------------------------------------------

// Without an OAuth client registered by the operator there is nothing to authorise
// against, and saying so is more useful than a broken redirect to Google.
func TestGmailAuthorizeNeedsAnOAuthClient(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/gmail/authorize", nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d, want 409", resp.StatusCode)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "OAuth") {
		t.Errorf("the message does not explain what is missing: %q", msg)
	}
}

func TestGmailAuthorizeBuildsAConsentURL(t *testing.T) {
	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.GmailClientID = "client-123"
		cfg.GmailClientSecret = "secret"
	})
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/gmail/authorize", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize: %d %v", resp.StatusCode, body)
	}

	raw, _ := body["url"].(string)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("the URL does not parse: %v", err)
	}
	if parsed.Host != "accounts.google.com" {
		t.Errorf("host = %q", parsed.Host)
	}
	q := parsed.Query()
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Error("the URL carries no PKCE challenge")
	}
	if q.Get("state") == "" {
		t.Error("the URL carries no state")
	}

	// The verifier is kept server-side against the user, never sent to the browser.
	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := ts.db.UserSettings(t.Context(), user.ID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	verifier, _ := settings["pkce_verifier"].(string)
	if verifier == "" {
		t.Fatal("no verifier was stored")
	}
	if strings.Contains(raw, verifier) {
		t.Error("the verifier was put in the URL handed to the browser")
	}
	if settings["pkce_state"] != q.Get("state") {
		t.Error("the stored state does not match the one in the URL")
	}
}

// The callback is where an attacker would try to plant somebody else's code. A
// mismatched, missing or expired state has to be refused.
func TestGmailCallbackChecksState(t *testing.T) {
	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.GmailClientID = "client-123"
		cfg.GmailClientSecret = "secret"
	})
	ts.bootstrap(t)
	ts.do(t, "POST", "/api/v1/gmail/authorize", nil)

	user, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, query, wantParam string
	}{
		{"no state", "?code=abc", "invalid"},
		{"no code", "?state=abc", "invalid"},
		{"wrong state", "?code=abc&state=forkert", "state"},
		{"user declined", "?error=access_denied", "access_denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A redirect, because the person is looking at a browser tab.
			client := &http.Client{
				Jar: ts.client.Jar,
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
			req, err := http.NewRequest("GET", ts.URL+"/oauth/gmail/callback"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusFound {
				t.Fatalf("status %d, want a redirect", resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); !strings.Contains(loc, tc.wantParam) {
				t.Errorf("redirected to %q, want it to mention %q", loc, tc.wantParam)
			}
		})
	}

	// And nothing was connected by any of that.
	settings, err := ts.db.UserSettings(t.Context(), user.ID, "gmail")
	if err != nil {
		t.Fatal(err)
	}
	if settings["refresh_token"] != nil {
		t.Error("a refresh token was stored by a rejected callback")
	}
}

// A connected mailbox has to be pollable without a browser, which is what the
// background job does. With nothing connected it is a no-op rather than an error.
func TestGmailSyncIsANoOpWhenNotConnected(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/gmail/sync", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sync: %d %v", resp.StatusCode, body)
	}
	if body["created"] != float64(0) {
		t.Errorf("created = %v with nothing connected", body["created"])
	}
}

// --- version ------------------------------------------------------------------------------

// The check is off unless the operator asked for it. A self-hosted app that reaches
// out without being told to has broken the deal its operator made.
func TestVersionCheckIsOffByDefault(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "GET", "/api/v1/version", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("version: %d %v", resp.StatusCode, body)
	}
	if body["disabled"] != true {
		t.Errorf("disabled = %v, want true on a fresh instance", body["disabled"])
	}
	if body["update_available"] == true {
		t.Error("an update was reported with checking turned off")
	}
	if body["current"] == nil {
		t.Error("the current version is not reported")
	}
}

// Only an administrator can do anything about an out-of-date server, so only an
// administrator is told.
func TestUpdateNoticeIsForAdministratorsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	member := ts.newUser(t, "medlem@example.dk", "Medlem")

	_, body := member.do(t, "GET", "/api/v1/version", nil)
	if body["update_available"] == true {
		t.Error("an ordinary member was told the server is out of date")
	}
	if body["latest"] != nil && body["latest"] != "" {
		t.Errorf("latest = %v for a non-admin", body["latest"])
	}
	// They still see which version they are on, which is what a bug report needs.
	if body["current"] == nil {
		t.Error("the current version is hidden from members")
	}
}

// --- MCP with the key in the URL --------------------------------------------------

// Claude's custom-connector dialog takes a URL and nothing else — no bearer
// token — so this is the only shape that can be configured from it.
func TestMCPAcceptsAKeyInTheQuery(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var userID string
	err := ts.db.QueryRowContext(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	key := ts.apiToken(t, userID)

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	resp, err := http.Post(ts.URL+"/mcp?key="+key, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("the response was not JSON-RPC: %v", err)
	}
	result, _ := decoded["result"].(map[string]any)
	if result == nil || result["protocolVersion"] == nil {
		t.Errorf("no protocol version was announced: %v", decoded)
	}
}

// The endpoint that broke it: /mcp used not to exist, so it fell through to the
// SPA fallback and answered 200 with the app shell. A connector pointed at it
// reported success and then failed to parse a page of HTML.
func TestMCPWithoutAKeyIsNotTheAppShell(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, url := range []string{ts.URL + "/mcp", ts.URL + "/mcp?key=not-a-verdande-token"} {
		resp, err := http.Post(url, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", url, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s: content-type %q — a connector cannot read that", url, ct)
		}
	}
}

// A revoked key stops working, like any other.
func TestMCPKeyStopsWorkingWhenRevoked(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	var userID string
	if err := ts.db.QueryRowContext(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	key := ts.apiToken(t, userID)

	var id string
	if err := ts.db.QueryRowContext(t.Context(), `SELECT id FROM api_tokens LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := ts.db.DeleteAPIToken(t.Context(), userID, id); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(ts.URL+"/mcp?key="+key, "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked key still works: status %d", resp.StatusCode)
	}
}

// The session cookie must NOT work here. This route sits outside the CSRF check,
// so a cookie-authenticated POST would be a cross-site request that acts as
// whoever is signed in — the exact hole the key-only rule exists to close.
func TestMCPWithKeyIgnoresTheSessionCookie(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// ts.client holds the session from bootstrap. No key in the URL.
	resp, err := ts.client.Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("a signed-in cookie authenticated a POST outside the CSRF check: status %d",
			resp.StatusCode)
	}
}

// --- mail to task ------------------------------------------------------------------

// The address used to come out as todo+…@localhost on any instance without a
// mail server, because VERDANDE_SMTP_FROM defaults to verdande@localhost and the
// domain was taken from it unconditionally. That address looks real, cannot
// receive anything, and gives no hint which of the two it is.
func TestMailAddressDoesNotClaimToBeLocalhost(t *testing.T) {
	ts := newTestServerWith(t, func(c *config.Config) {
		c.BaseURL = "https://todo.example.dk"
		// Left at the default, as an instance with no mail server has it.
		c.SMTP.From = "verdande@localhost"
	})
	ts.bootstrap(t)

	_, body := ts.do(t, "GET", "/api/v1/mail-address", nil)
	address, _ := body["address"].(string)

	if strings.HasSuffix(address, "@localhost") {
		t.Errorf("address = %q — that can never receive mail", address)
	}
	if !strings.HasSuffix(address, "@todo.example.dk") {
		t.Errorf("address = %q, want the public hostname", address)
	}
	if body["configured"] != false {
		t.Error("no SMTP host is set, so configured should be false")
	}
}

// A real sender address still wins: the mail server's own domain is the one that
// can actually route this, and the web address may be a tunnel hostname.
func TestMailAddressPrefersTheConfiguredSender(t *testing.T) {
	ts := newTestServerWith(t, func(c *config.Config) {
		c.BaseURL = "https://tunnel-abc123.example.net"
		c.SMTP.Host = "mail.firma.dk"
		c.SMTP.From = "verdande@firma.dk"
	})
	ts.bootstrap(t)

	_, body := ts.do(t, "GET", "/api/v1/mail-address", nil)
	address, _ := body["address"].(string)

	if !strings.HasSuffix(address, "@firma.dk") {
		t.Errorf("address = %q, want the sender's domain", address)
	}
	if body["configured"] != true {
		t.Error("an SMTP host is set, so configured should be true")
	}
}

// A 200 with the app shell is an answer that says "yes, that exists" to anything
// that asks. For /.well-known/ that is actively harmful: it is a reserved
// namespace, and a client probing it is asking a yes-or-no question.
//
// Claude asks /.well-known/oauth-authorization-server before connecting to an
// MCP server. It read the shell as "there is an authorization server here" and
// then failed trying to register with it, reporting a broken sign-in service on
// a server that has none and needs none.
func TestWellKnownDoesNotAnswerWithTheAppShell(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/.well-known/openid-configuration",
		"/.well-known/anything-at-all",
	} {
		resp, err := ts.client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
		if strings.Contains(string(body), "<!doctype html") {
			t.Errorf("%s: answered with the app shell", path)
		}
	}
}

// The one well-known path that is real still works — it is a route, not a
// fallthrough, so the rule above must not have swallowed it.
func TestWellKnownCalDAVStillRedirects(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(ts.URL + "/.well-known/caldav")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		t.Error("CalDAV discovery was caught by the well-known rule")
	}
}

// --- what the API accepts, it must actually do -------------------------------------

// project_id on an update used to be validated and then dropped: the destination
// was permission-checked, which reads as "understood", and the task never moved
// while the answer said OK. Both the REST path and the MCP tool did it.
func TestUpdateMovesTheTaskToAnotherProject(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Firma"})
	projectID := project["id"].(string)

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "betal moms"})
	taskID := task["id"].(string)
	if task["project_id"] == projectID {
		t.Fatal("the task started in the destination; the test proves nothing")
	}

	resp, updated := ts.do(t, "PATCH", "/api/v1/tasks/"+taskID,
		map[string]any{"project_id": projectID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: status %d, body %v", resp.StatusCode, updated)
	}
	if updated["project_id"] != projectID {
		t.Errorf("project_id = %v, want %v — the move was accepted and not made",
			updated["project_id"], projectID)
	}

	// And it really is there, not merely reported as there.
	_, reread := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	if reread["project_id"] != projectID {
		t.Errorf("after re-reading, project_id = %v", reread["project_id"])
	}
}

func TestMCPUpdateMovesTheTaskToAnotherProject(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Firma"})
	projectID := project["id"].(string)

	created := ts.callTool(t, "create_task", map[string]any{"text": "betal moms"})
	taskID := created["id"].(string)

	moved := ts.callTool(t, "update_task", map[string]any{
		"task_id": taskID, "project_id": projectID,
	})
	if moved["project_id"] != projectID {
		t.Errorf("project_id = %v, want %v", moved["project_id"], projectID)
	}
}

// A project you cannot write to is refused rather than quietly ignored.
func TestUpdateRefusesAProjectYouCannotWriteTo(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	other := ts.newUser(t, "anden@example.dk", "Anden")
	_, theirs := other.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Deres"})

	_, task := ts.do(t, "POST", "/api/v1/tasks", map[string]any{"content": "min opgave"})
	taskID := task["id"].(string)
	mine := task["project_id"]

	resp, _ := ts.do(t, "PATCH", "/api/v1/tasks/"+taskID,
		map[string]any{"project_id": theirs["id"]})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status %d, want 404", resp.StatusCode)
	}

	_, reread := ts.do(t, "GET", "/api/v1/tasks/"+taskID, nil)
	if reread["project_id"] != mine {
		t.Error("the task moved into somebody else's project")
	}
}

// An unknown #project is still not an error — the thought is kept rather than
// thrown away over a typo — but it is no longer silent. It used to vanish from
// the title while the task went to the Inbox, with nothing saying why.
func TestQuickAddSaysWhenTheProjectDoesNotExist(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/tasks/quick-add",
		map[string]any{"text": "Lav plakat #3Dekoration"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d, want 201 — the task should still be created", resp.StatusCode)
	}
	if body["unknown_project"] != "3Dekoration" {
		t.Errorf("unknown_project = %v, want %q", body["unknown_project"], "3Dekoration")
	}
	if body["content"] != "Lav plakat" {
		t.Errorf("content = %v", body["content"])
	}
}

func TestQuickAddSaysNothingWhenTheProjectExists(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Dekoration"})

	_, body := ts.do(t, "POST", "/api/v1/tasks/quick-add",
		map[string]any{"text": "Lav plakat #Dekoration"})
	if _, present := body["unknown_project"]; present {
		t.Errorf("reported an unknown project for one that exists: %v", body)
	}
}

func TestMCPCreateSaysWhenTheProjectDoesNotExist(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	created := ts.callTool(t, "create_task", map[string]any{"text": "Lav plakat #3Dekoration"})
	note, _ := created["note"].(string)
	if !strings.Contains(note, "3Dekoration") {
		t.Errorf("note = %q, want it to name the project that does not exist", note)
	}
}

// Sub-tasks were unreachable over MCP: create_task had no parent_id, so a parent
// with seven children arrived as eight unrelated tasks.
func TestMCPCreateMakesSubtasks(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, project := ts.do(t, "POST", "/api/v1/projects", map[string]string{"name": "Firma"})
	parent := ts.callTool(t, "create_task", map[string]any{
		"content": "Runen v2", "project_id": project["id"],
	})
	parentID := parent["id"].(string)

	child := ts.callTool(t, "create_task", map[string]any{
		"content": "Tegn ikonet", "parent_id": parentID,
	})
	if child["parent_id"] != parentID {
		t.Errorf("parent_id = %v, want %v", child["parent_id"], parentID)
	}
	// A sub-task joins its parent's project whatever else was asked for.
	if child["project_id"] != project["id"] {
		t.Errorf("project_id = %v, want the parent's %v", child["project_id"], project["id"])
	}

	_, listed := ts.do(t, "GET", "/api/v1/tasks?parent_id="+parentID, nil)
	if tasks, _ := listed["tasks"].([]any); len(tasks) != 1 {
		t.Errorf("the parent has %d sub-tasks, want 1", len(tasks))
	}
}

func TestMCPCreateRefusesASecondLevelOfSubtask(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	parent := ts.callTool(t, "create_task", map[string]any{"content": "Runen v2"})
	child := ts.callTool(t, "create_task", map[string]any{
		"content": "Tegn ikonet", "parent_id": parent["id"],
	})

	// A tool that refuses reports it inside the result with isError, not as a
	// JSON-RPC error — the call itself was well formed, the arguments were not.
	response := ts.rpc(t, "tools/call", map[string]any{
		"name":      "create_task",
		"arguments": map[string]any{"content": "Endnu dybere", "parent_id": child["id"]},
	})
	result, _ := response["result"].(map[string]any)
	if result == nil || result["isError"] != true {
		t.Errorf("a sub-task of a sub-task was accepted: %v", response)
	}
}

// The Gmail OAuth client, enterable rather than deployed.
//
// The registration itself cannot be avoided — Google issues no Gmail access to an
// unregistered client — but having to edit a Rune's manifest and recreate the
// container to paste the result can be, and that is what this covers.
func TestTheGmailClientCanBeSetFromTheInterface(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	_, empty := ts.do(t, "GET", "/api/v1/gmail/client", nil)
	if empty["client_id"] != "" || empty["has_secret"] != false {
		t.Errorf("a fresh instance already has a client: %v", empty)
	}
	if empty["from_env"] != false {
		t.Errorf("from_env = %v with nothing in the environment", empty["from_env"])
	}
	// The single most likely thing to be wrong, so it is spelled out rather than
	// left for somebody to reconstruct.
	if empty["redirect_uri"] != "http://localhost/oauth/gmail/callback" {
		t.Errorf("redirect_uri = %v", empty["redirect_uri"])
	}

	resp, _ := ts.do(t, "PUT", "/api/v1/gmail/client", map[string]any{
		"client_id": "1234.apps.googleusercontent.com", "client_secret": "hemmelig",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("save: status %d", resp.StatusCode)
	}

	_, saved := ts.do(t, "GET", "/api/v1/gmail/client", nil)
	if saved["client_id"] != "1234.apps.googleusercontent.com" {
		t.Errorf("client_id = %v", saved["client_id"])
	}
	if saved["has_secret"] != true {
		t.Error("has_secret is false after saving one")
	}
	// Never read back. A settings page that repopulates a password field is one
	// that will eventually leak into a screenshot.
	if _, present := saved["client_secret"]; present {
		t.Error("the secret came back in the response")
	}

	// Saving only the id must not clear the secret — otherwise correcting a typo
	// silently breaks the connection.
	ts.do(t, "PUT", "/api/v1/gmail/client", map[string]any{
		"client_id": "5678.apps.googleusercontent.com",
	})
	_, again := ts.do(t, "GET", "/api/v1/gmail/client", nil)
	if again["client_id"] != "5678.apps.googleusercontent.com" || again["has_secret"] != true {
		t.Errorf("saving the id alone lost the secret: %v", again)
	}

	// And the authorise call now has something to build a URL with, which is the
	// whole point: one click, and Google's consent screen.
	resp, body := ts.do(t, "POST", "/api/v1/gmail/authorize", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize with a stored client: status %d, body %v", resp.StatusCode, body)
	}
	if url, _ := body["url"].(string); !strings.Contains(url, "5678.apps.googleusercontent.com") {
		t.Errorf("the consent URL does not carry the stored client: %v", body["url"])
	}
}

// The environment wins, and says so. A value in the Rune's manifest is applied on
// every recreate, so a form that could quietly override it would make the manifest
// a lie.
func TestTheEnvironmentsGmailClientCannotBeOverwritten(t *testing.T) {
	ts := newTestServerWith(t, func(c *config.Config) {
		c.GmailClientID = "fra-miljøet.apps.googleusercontent.com"
		c.GmailClientSecret = "også-fra-miljøet"
	})
	ts.bootstrap(t)

	_, shown := ts.do(t, "GET", "/api/v1/gmail/client", nil)
	if shown["from_env"] != true {
		t.Errorf("from_env = %v with the environment set", shown["from_env"])
	}
	if shown["client_id"] != "fra-miljøet.apps.googleusercontent.com" {
		t.Errorf("client_id = %v", shown["client_id"])
	}

	resp, _ := ts.do(t, "PUT", "/api/v1/gmail/client", map[string]any{
		"client_id": "min-egen", "client_secret": "min-egen",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("overwriting the environment: status %d, want 409", resp.StatusCode)
	}

	_, after := ts.do(t, "GET", "/api/v1/gmail/client", nil)
	if after["client_id"] != "fra-miljøet.apps.googleusercontent.com" {
		t.Errorf("the environment's client was replaced: %v", after["client_id"])
	}
}

// Administrators only, and sessions only: this is the instance's identity to
// Google, and a leaked token must not be able to read or replace it.
func TestTheGmailClientIsAdminsOnlyAndSessionsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	for _, c := range []struct {
		method string
		body   any
	}{{"GET", nil}, {"PUT", map[string]any{"client_id": "x"}}} {
		if resp, _ := other.do(t, c.method, "/api/v1/gmail/client", c.body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s as an ordinary user: status %d, want 403", c.method, resp.StatusCode)
		}
	}

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, admin.ID)
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/gmail/client", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("with an admin's API token: status %d, want 403", resp.StatusCode)
	}
}
