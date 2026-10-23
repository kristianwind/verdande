package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func (ts *testServer) corsDo(t *testing.T, method, path, origin string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestCORSAnswersTheAppAndNobodyElse(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	preflight := map[string]string{
		"Access-Control-Request-Method":  "POST",
		"Access-Control-Request-Headers": "authorization, content-type",
	}

	resp := ts.corsDo(t, "OPTIONS", "/api/v1/projects", "capacitor://localhost", preflight)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("app preflight: status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "capacitor://localhost" {
		t.Errorf("app preflight: Allow-Origin %q", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Errorf("app preflight: Allow-Headers %q lacks Authorization", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("credentials allowed (%q): the app uses a bearer, never a cookie", got)
	}

	// An ordinary request from the app can be read.
	resp = ts.corsDo(t, "GET", "/api/v1/ping", "capacitor://localhost", nil)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "capacitor://localhost" {
		t.Errorf("app GET: Allow-Origin %q", got)
	}

	// Anybody else gets nothing, so the browser keeps them from reading.
	for _, origin := range []string{"https://evil.example", "http://localhost", "capacitor://evil", "null"} {
		resp = ts.corsDo(t, "OPTIONS", "/api/v1/projects", origin, preflight)
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: preflight answered with Allow-Origin %q", origin, got)
		}
		resp = ts.corsDo(t, "GET", "/api/v1/ping", origin, nil)
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: GET answered with Allow-Origin %q", origin, got)
		}
	}
}

// The app asks this before anyone has signed in, to tell a Verdande from a
// router's login page.
func TestPingNamesTheProductAndEditionButNoVersion(t *testing.T) {
	ts := newTestServer(t)
	anonymous := &testServer{Server: ts.Server, db: ts.db, client: newJarClient(t)}
	resp, body := anonymous.do(t, "GET", "/api/v1/ping", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping: %d", resp.StatusCode)
	}
	if body["product"] != "verdande" || body["edition"] != "full" || body["api"] != float64(1) {
		t.Errorf("ping = %v", body)
	}
	if _, ok := body["version"]; ok {
		t.Error("ping reveals the version to anybody")
	}
}

func dialWS(ctx context.Context, url, origin string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	return websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: h})
}

func TestTheAppOpensLiveSyncWithATicket(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	_, body := appLogin(t, ts)
	token := body["session_token"].(string)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Without a ticket or a cookie: refused.
	if _, resp, err := dialWS(ctx, wsURL, "capacitor://localhost"); err == nil {
		t.Fatal("a socket opened with no credential")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no credential: %v", resp)
	}

	resp, body := ts.appDo(t, token, "POST", "/api/v1/ws-ticket", nil)
	ticket, _ := body["ticket"].(string)
	if resp.StatusCode != http.StatusOK || ticket == "" {
		t.Fatalf("ws-ticket: %d %v", resp.StatusCode, body)
	}

	conn, _, err := dialWS(ctx, wsURL+"?ticket="+ticket, "capacitor://localhost")
	if err != nil {
		t.Fatalf("dial with a ticket: %v", err)
	}
	defer conn.CloseNow()

	// And it is the right person's stream: their own new project arrives on it.
	ts.appDo(t, token, "POST", "/api/v1/projects", map[string]string{"name": "Live fra telefonen"})
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(msg), "Live fra telefonen") {
		t.Errorf("first event was %s", msg)
	}

	// Spent: the same ticket does not open a second socket.
	if _, resp, err := dialWS(ctx, wsURL+"?ticket="+ticket, "capacitor://localhost"); err == nil {
		t.Error("a ticket opened two sockets")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("reused ticket: %v", resp)
	}
}

func TestATicketExpires(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	_, body := appLogin(t, ts)
	_, body = ts.appDo(t, body["session_token"].(string), "POST", "/api/v1/ws-ticket", nil)
	ticket := body["ticket"].(string)

	ts.api.wsTickets.mu.Lock()
	v := ts.api.wsTickets.m[ticket]
	v.expires = time.Now().Add(-time.Second)
	ts.api.wsTickets.m[ticket] = v
	ts.api.wsTickets.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws?ticket=" + ticket
	if _, resp, err := dialWS(ctx, wsURL, "capacitor://localhost"); err == nil {
		t.Error("an expired ticket opened a socket")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expired ticket: %v", resp)
	}
}

// The cookie path is unchanged: a foreign page cannot open a socket that rides
// on the cookie, and the instance's own page can.
func TestACookieSocketStillChecksTheOrigin(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	var cookie string
	for _, c := range ts.client.Jar.Cookies(mustURL(t, ts.URL)) {
		if c.Name == sessionCookieInsecure {
			cookie = c.Value
		}
	}
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/v1/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dial := func(origin string) error {
		h := http.Header{}
		h.Set("Origin", origin)
		h.Set("Cookie", sessionCookieInsecure+"="+cookie)
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: h})
		if err == nil {
			conn.CloseNow()
		}
		return err
	}
	if err := dial("https://evil.example"); err == nil {
		t.Error("a foreign page opened a socket on the cookie")
	}
	if err := dial("capacitor://localhost"); err == nil {
		t.Error("the app origin opened a socket on a cookie, without a ticket")
	}
	if err := dial("http://localhost"); err != nil {
		t.Errorf("control: the instance's own origin: %v", err)
	}
}
