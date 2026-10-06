package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/store"
)

// The notes edition is a different program, and this is what makes that true.
//
// A gate in a route tree is the easiest thing in the world to believe and the
// hardest to see: the code reads `if full {`, somebody confirms the mechanism
// exists, and nothing ever asks whether it covers the routes it was written for.
// So this does not check that a gate is *present*. It walks both routers and
// compares the two sets of routes they actually serve.
//
// The difference is written down below. A route added later that differs between
// the editions — in either direction — fails here until somebody says which half
// it belongs to. That is the point: the list is the decision, and the walk is
// what keeps the decision honest.
func TestNotesEditionServesOnlyItsOwnRoutes(t *testing.T) {
	full := routesOf(t, config.EditionFull)
	notes := routesOf(t, config.EditionNotes)

	// Every route the full edition has and the notes edition does not. Written as
	// the whole list rather than a prefix rule: a prefix is a description of what
	// somebody meant, and this has to be a description of what is there.
	withheld := map[string]bool{}
	for _, r := range []string{
		"DELETE /api/v1/filters/{filterID}",
		"DELETE /api/v1/labels/{labelID}",
		"DELETE /api/v1/reminders/{reminderID}",
		"DELETE /api/v1/tasks/{taskID}",
		"GET /api/v1/delegated",
		"GET /api/v1/filters",
		"GET /api/v1/filters/preview",
		"GET /api/v1/filters/{filterID}/tasks",
		"GET /api/v1/labels",
		"GET /api/v1/tasks",
		"GET /api/v1/tasks/quick-add/preview",
		"GET /api/v1/tasks/{taskID}",
		"GET /api/v1/tasks/{taskID}/comments",
		"GET /api/v1/tasks/{taskID}/reminders",
		"GET /api/v1/upcoming",
		"PATCH /api/v1/filters/{filterID}",
		"PATCH /api/v1/labels/{labelID}",
		"PATCH /api/v1/tasks/{taskID}",
		"POST /api/v1/filters",
		"POST /api/v1/labels",
		"POST /api/v1/tasks",
		"POST /api/v1/tasks/quick-add",
		"POST /api/v1/tasks/{taskID}/attachments",
		"POST /api/v1/tasks/{taskID}/comments",
		"POST /api/v1/tasks/{taskID}/complete",
		"POST /api/v1/tasks/{taskID}/move",
		"POST /api/v1/tasks/{taskID}/reminders",
		"POST /api/v1/tasks/{taskID}/reopen",
		"POST /api/v1/tasks/{taskID}/snooze",
	} {
		withheld[r] = true
	}

	var unexpected, stillThere []string
	for r := range full {
		if notes[r] {
			if withheld[r] {
				stillThere = append(stillThere, r)
			}
			continue
		}
		if !withheld[r] {
			unexpected = append(unexpected, r)
		}
	}
	// And the other direction: the notes edition must not grow a route the full
	// one lacks. A gate that adds is not a gate either.
	var onlyInNotes []string
	for r := range notes {
		if !full[r] {
			onlyInNotes = append(onlyInNotes, r)
		}
	}

	sort.Strings(unexpected)
	sort.Strings(stillThere)
	sort.Strings(onlyInNotes)
	for _, r := range unexpected {
		t.Errorf("withheld from the notes edition but not written down here: %s", r)
	}
	for _, r := range stillThere {
		t.Errorf("listed as withheld and still served by the notes edition: %s", r)
	}
	for _, r := range onlyInNotes {
		t.Errorf("served only by the notes edition: %s", r)
	}

	// Printed on the green as well as the red. "The editions differ correctly" over
	// zero withheld routes and over twenty-nine are the same pass, and only one of
	// them means anything — so the number is on screen either way.
	t.Logf("full: %d routes · notes: %d · withheld: %d", len(full), len(notes), len(full)-len(notes))

	// The half that stops this being a deletion rather than a gate: what the notes
	// edition is FOR has to still be there.
	for _, must := range []string{
		"GET /api/v1/notes",
		"POST /api/v1/notes",
		"GET /api/v1/export/notes.zip",
		"GET /api/v1/projects", // a notebook, in this edition
		"GET /api/v1/search",
		"GET /api/v1/auth/me",
	} {
		if !notes[must] {
			t.Errorf("the notes edition does not serve %s — that is a deletion, not a gate", must)
		}
	}
}

// routesOf builds a server at one edition and returns every route it serves.
func routesOf(t *testing.T, edition string) map[string]bool {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(&config.Config{
		BaseURL: "http://127.0.0.1", DataDir: t.TempDir(), Edition: edition,
		SessionTTL: time.Hour, InviteTTL: time.Hour, ResetTTL: time.Hour,
	}, db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	out := map[string]bool{}
	err = chi.Walk(srv.router,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if route != "/" {
				route = strings.TrimSuffix(route, "/")
			}
			out[method+" "+route] = true
			return nil
		})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(out) < 50 {
		// A walk that found almost nothing would make every comparison below pass.
		t.Fatalf("only %d routes at edition %q — the walk found nothing to compare", len(out), edition)
	}
	return out
}
