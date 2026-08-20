package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kristianwind/verdande/internal/config"
)

// A panel that never answers must not become this server's problem. The container
// is being asked to stop, so the reply may legitimately never arrive — but the
// request has to end either way, or whatever sits in front answers for it.
func TestAPanelThatNeverAnswersStillEndsTheRequest(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drained first. A real server reads what it was sent, and one that does not
		// never learns the caller has hung up — httptest.Server.Close then waits for
		// a handler that will never return, and the whole binary times out instead
		// of the one request.
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer panel.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.PanelURL = panel.URL
		cfg.PanelToken = "test-token"
		cfg.PanelServerID = "server-1"
	})
	ts.bootstrap(t)

	// Its own client: the shared one gives up at ten seconds, which is the very
	// budget under test, so it could not tell a pass from a failure.
	req, _ := http.NewRequest("POST", ts.URL+"/api/v1/panel/restart", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for _, c := range ts.client.Jar.Cookies(mustParse(t, ts.URL)) {
		req.AddCookie(c)
	}

	started := time.Now()
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("the request never came back (%v after %s)", err, time.Since(started).Round(time.Second))
	}
	defer resp.Body.Close()
	took := time.Since(started)

	if took > 20*time.Second {
		t.Errorf("the restart took %s; that is long enough for a proxy to answer first",
			took.Round(time.Second))
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status %d, want 202 — a panel that went quiet is the expected case, "+
			"not a failure", resp.StatusCode)
	}
	t.Logf("came back in %s with %d", took.Round(time.Millisecond), resp.StatusCode)
}

// And a panel that says no must say so in this server's own words, not in a proxy's.
func TestAPanelThatRefusesSaysSoAsJSON(t *testing.T) {
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer panel.Close()

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.PanelURL = panel.URL
		cfg.PanelToken = "wrong-token"
		cfg.PanelServerID = "server-1"
	})
	ts.bootstrap(t)

	resp, body := ts.do(t, "POST", "/api/v1/panel/restart", nil)
	if resp.StatusCode != StatusUpstreamRefused {
		t.Errorf("status %d, want 502: %s", resp.StatusCode, body)
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "403") {
		t.Errorf("the reply does not say what the panel answered: %v", body)
	}
}

// Forty copies of one mail arrived in a single second because two runs read the
// same marker before either wrote a new one — somebody pressing "Fetch now" while
// the ten-minute sweep was already inside the same mailbox.
//
// The lock is what stops it, and a lock is only worth having if something tries
// the door: this runs the two concurrently on purpose.
func TestTwoRunsOfOneMailboxDoNotOverlap(t *testing.T) {
	// A bare server: the lock needs nothing but its own zero value, and building a
	// database and a session around it would only make the test slower to read.
	srv := &Server{}

	var inside, most int
	var mu sync.Mutex
	release := make(chan struct{})

	// Stands in for a run: takes the lock, notes how many are inside, and holds it
	// until told to let go.
	run := func(done chan<- struct{}) {
		unlock := srv.lockSync("box-1")
		mu.Lock()
		inside++
		if inside > most {
			most = inside
		}
		mu.Unlock()

		<-release

		mu.Lock()
		inside--
		mu.Unlock()
		unlock()
		done <- struct{}{}
	}

	first, second := make(chan struct{}), make(chan struct{})
	go run(first)
	go run(second)

	// Long enough that both would be inside if nothing kept them apart.
	time.Sleep(100 * time.Millisecond)
	close(release)
	<-first
	<-second

	if most != 1 {
		t.Errorf("%d runs were inside the same mailbox at once; that is how one mail "+
			"becomes forty tasks", most)
	}

	// And two different mailboxes must not wait for each other: one slow host is
	// not everybody's problem.
	a := srv.lockSync("box-a")
	done := make(chan struct{})
	go func() {
		srv.lockSync("box-b")()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("a second mailbox waited for the first")
	}
	a()
}
