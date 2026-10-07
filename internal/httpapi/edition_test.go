package httpapi

import (
	"context"
	"encoding/json"
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
	//
	// And note what this list could NOT catch, because it is the shape of mistake
	// that outlived the first version of this test. The list went green at 29
	// routes for two merges while CalDAV, the ICS feed, the inbox hook, four task
	// exports, both task imports, the project templates, the sections and six AI
	// task endpoints were all still served by the notes edition — because the test
	// compares the router against THIS LIST, and the list was written from the
	// routes somebody had thought of. It is a drift check, and it cannot be an
	// is-it-complete check.
	//
	// So when a route is added that one edition should not have, the question to
	// ask is not "does the walk still pass" but "can this endpoint do anything at
	// all without the half it is being withheld from". Three of the ones above were
	// settled by reading rather than by the name: a template turned out to create
	// tasks (CreateProjectFromTemplate), `section_id` turned out to exist on
	// `tasks` and not on `notes`, and add_comment turned out to hang off a task.
	withheld := map[string]bool{}
	for _, r := range []string{
		"DELETE /api/v1/filters/{filterID}",
		"DELETE /api/v1/labels/{labelID}",
		"DELETE /api/v1/reminders/{reminderID}",
		"DELETE /api/v1/sections/{sectionID}",
		"DELETE /api/v1/tasks/{taskID}",
		"DELETE /api/v1/templates/{templateID}",
		"DELETE /caldav/{userID}/{projectID}/{taskFile}",
		"GET /.well-known/caldav",
		"GET /api/v1/ai/plan",
		"GET /api/v1/delegated",
		"GET /api/v1/export/projects.zip",
		"GET /api/v1/export/projects/{projectID}.csv",
		"GET /api/v1/export/projects/{projectID}.ics",
		"GET /api/v1/export/tasks.ics",
		"GET /api/v1/feed",
		"GET /api/v1/filters",
		"GET /api/v1/filters/preview",
		"GET /api/v1/filters/{filterID}/tasks",
		"GET /api/v1/labels",
		"GET /api/v1/projects/{projectID}/sections",
		"GET /api/v1/tasks",
		"GET /api/v1/tasks/quick-add/preview",
		"GET /api/v1/tasks/{taskID}",
		"GET /api/v1/tasks/{taskID}/comments",
		"GET /api/v1/tasks/{taskID}/reminders",
		"GET /api/v1/templates",
		"GET /api/v1/upcoming",
		"GET /caldav/{userID}/{projectID}/{taskFile}",
		"GET /ics/{token}",
		"OPTIONS /caldav/*",
		"PATCH /api/v1/filters/{filterID}",
		"PATCH /api/v1/labels/{labelID}",
		"PATCH /api/v1/sections/{sectionID}",
		"PATCH /api/v1/tasks/{taskID}",
		"POST /api/v1/ai/inbox/tidy",
		"POST /api/v1/ai/inbox/tidy/apply",
		"POST /api/v1/ai/plan/now",
		"POST /api/v1/ai/tasks/{taskID}/split",
		"POST /api/v1/feed/rotate",
		"POST /api/v1/filters",
		"POST /api/v1/import/csv",
		"POST /api/v1/import/todoist",
		"POST /api/v1/labels",
		"POST /api/v1/projects/{projectID}/sections",
		"POST /api/v1/projects/{projectID}/sections/reorder",
		"POST /api/v1/tasks",
		"POST /api/v1/tasks/quick-add",
		"POST /api/v1/tasks/{taskID}/attachments",
		"POST /api/v1/tasks/{taskID}/comments",
		"POST /api/v1/tasks/{taskID}/complete",
		"POST /api/v1/tasks/{taskID}/move",
		"POST /api/v1/tasks/{taskID}/reminders",
		"POST /api/v1/tasks/{taskID}/reopen",
		"POST /api/v1/tasks/{taskID}/snooze",
		"POST /api/v1/templates",
		"POST /api/v1/templates/{templateID}/use",
		"POST /inbound/hook/{token}",
		"PROPFIND /caldav",
		"PROPFIND /caldav/{userID}",
		"PROPFIND /caldav/{userID}/{projectID}",
		"PUT /api/v1/ai/plan",
		"PUT /caldav/{userID}/{projectID}/{taskFile}",
		"REPORT /caldav/{userID}/{projectID}",
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
		"PATCH /api/v1/notes/{noteID}",
		"DELETE /api/v1/notes/{noteID}",
		// Deling er hele grunden til at det er et selvstændigt produkt frem for en
		// enkeltbrugers notesblok, så den halvdel skal navngives her.
		"POST /api/v1/notes/{noteID}/shares",
		"GET /api/v1/notes/{noteID}/shares",
		"POST /api/v1/notes/{noteID}/attachments",
		"POST /api/v1/notes/import",
		"GET /api/v1/export/notes.zip",
		"GET /api/v1/export/account",
		"POST /api/v1/ai/notes/{noteID}/actions",
		"GET /api/v1/projects", // a notebook, in this edition
		"GET /api/v1/search",
		"GET /api/v1/auth/me",
		// 34 ruter blev lukket i den anden omgang af denne port. Den her linje er
		// den, der afgør, at det var en port: MCP-endepunktet er stadig der, og det
		// er værktøjslisten bagved, der er kortere.
		"POST /api/v1/mcp",
		"POST /mcp",
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

// The interface cannot gate itself on something it is not told, and it has to be
// told before anybody is signed in — an instance's shape is not a permission.
func TestSetupStateSaysWhichEdition(t *testing.T) {
	for _, tc := range []struct{ edition, want string }{
		{config.EditionFull, "full"},
		{config.EditionNotes, "notes"},
		{"", "full"}, // a config built by hand is the program as it has always been
	} {
		ts := newTestServerWith(t, func(c *config.Config) { c.Edition = tc.edition })

		_, body := ts.do(t, "GET", "/api/v1/auth/setup", nil)
		if got := body["edition"]; got != tc.want {
			t.Errorf("edition %q reported as %v, want %q", tc.edition, got, tc.want)
		}
		// And the field it has always had is still there: adding one must not
		// quietly replace the answer the sign-in screen depends on.
		if _, ok := body["needs_setup"]; !ok {
			t.Errorf("edition %q: needs_setup is gone from the answer", tc.edition)
		}
	}
}

// Search is mounted in both editions — notes and notebooks have to be findable —
// so it is the one door that could still open onto the half the router just shut.
//
// Not a theoretical case. An instance converted from a full one still has the
// tasks in its database, and ⌘K would be the only place they turned up.
func TestNotesEditionSearchFindsNoTasks(t *testing.T) {
	for _, tc := range []struct {
		edition string
		want    int
	}{
		{config.EditionFull, 1},
		{config.EditionNotes, 0},
	} {
		ts := newTestServerWith(t, func(c *config.Config) { c.Edition = tc.edition })
		ts.bootstrap(t)

		_, me := ts.do(t, "GET", "/api/v1/auth/me", nil)
		userID, _ := me["id"].(string)
		if userID == "" {
			t.Fatalf("no user: %v", me)
		}

		// Created through the store rather than the API: in the notes edition there
		// is no route to create one, and the row this is about is the row that is
		// already there.
		inbox, err := ts.db.InboxID(context.Background(), userID)
		if err != nil {
			t.Fatal(err)
		}
		task := &store.Task{ProjectID: inbox, Content: "Kvartalsregnskabet", CreatedBy: userID}
		if err := ts.db.CreateTask(context.Background(), task, nil); err != nil {
			t.Fatal(err)
		}

		_, body := ts.do(t, "GET", "/api/v1/search?q=Kvartalsregnskabet", nil)
		found, _ := body["tasks"].([]any)
		if len(found) != tc.want {
			t.Errorf("edition %q: search returned %d tasks, want %d", tc.edition, len(found), tc.want)
		}
		// The full edition's number is the control: a search that finds nothing at
		// all would satisfy the notes case for the wrong reason.
		if tc.edition == config.EditionFull && len(found) == 0 {
			t.Errorf("the full edition found no tasks either — this proves nothing about the notes one")
		}
	}
}

// TestNotesEditionOffersNoTaskToolsOverMCP.
//
// The same hole as the routes, through a different door — and it was still open
// after the first gate. An MCP server advertises its tools, so a model connected
// to a notes instance would be told it can create_task, call it, get something
// back, and tell the person their task was filed.
//
// Asked through tools/list rather than by reading the server's own slice: that is
// what a client actually sees, and the slice is unexported for the same reason.
func TestNotesEditionOffersNoTaskToolsOverMCP(t *testing.T) {
	taskTools := []string{"search_tasks", "create_task", "update_task", "complete_task", "add_comment"}
	noteTools := []string{"search_notes", "create_note", "update_note"}

	for _, edition := range []string{config.EditionFull, config.EditionNotes} {
		t.Run(edition, func(t *testing.T) {
			names := mcpToolNames(t, edition)

			// Both editions: the shared tool, and the notes half.
			for _, want := range append([]string{"list_projects"}, noteTools...) {
				if !names[want] {
					t.Errorf("%s edition does not offer %s", edition, want)
				}
			}
			for _, task := range taskTools {
				switch edition {
				case config.EditionFull:
					if !names[task] {
						t.Errorf("the full edition lost %s — that is a deletion, not a gate", task)
					}
				default:
					if names[task] {
						t.Errorf("the notes edition offers %s over MCP", task)
					}
				}
			}
			t.Logf("%s: %d tools", edition, len(names))
		})
	}
}

// mcpToolNames asks one edition's MCP server for its tool list.
func mcpToolNames(t *testing.T, edition string) map[string]bool {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := &config.Config{BaseURL: "http://localhost", DataDir: t.TempDir(), SessionTTL: time.Hour, Edition: edition}
	srv := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	resp := srv.mcp.Handle(context.Background(), "nobody", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if resp == nil {
		t.Fatal("tools/list got no response")
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("tools/list: %v — %s", err, raw)
	}
	if len(parsed.Result.Tools) == 0 {
		t.Fatalf("tools/list returned no tools at all: %s", raw)
	}
	names := map[string]bool{}
	for _, tool := range parsed.Result.Tools {
		names[tool.Name] = true
	}
	return names
}
