package httpapi

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/config"
	"github.com/kristianwind/verdande/internal/store"
)

// docs/openapi.yaml is written by hand, which means it can drift from the router
// the moment somebody adds an endpoint. This walks the real router and compares
// the two in both directions: a route with nothing written about it, and a path
// documented that no longer exists.
//
// The spec is read with a small scanner rather than a YAML library. It needs two
// things — which paths are listed and which methods each one has — and both live
// at fixed indentation in a document this file also owns. That is worth more than
// a dependency in a project whose whole shape is "one static binary".

// specRoutes reads "method path" pairs out of the `paths:` section.
func specRoutes(t *testing.T) map[string]bool {
	t.Helper()

	f, err := os.Open(filepath.Join("..", "..", "docs", "openapi.yaml"))
	if err != nil {
		t.Fatalf("open spec: %v", err)
	}
	defer f.Close()

	methods := map[string]bool{
		"get": true, "post": true, "put": true, "patch": true, "delete": true,
		"head": true, "options": true, "trace": true,
	}

	routes := map[string]bool{}
	inPaths := false
	path := ""

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		// A top-level key ends the paths section.
		if !strings.HasPrefix(line, " ") {
			inPaths = strings.HasPrefix(line, "paths:")
			continue
		}
		if !inPaths {
			continue
		}

		switch {
		// Two spaces: a path.
		case strings.HasPrefix(line, "  /"):
			path = strings.TrimSuffix(strings.TrimSpace(line), ":")
		// Four spaces: a key under it, which is a method or `parameters`.
		case path != "" && strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     "):
			key := strings.TrimSuffix(strings.TrimSpace(line), ":")
			if methods[key] {
				routes[strings.ToUpper(key)+" "+path] = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read spec: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("no paths were read out of the spec — the scanner and the file disagree")
	}
	return routes
}

// undocumented is what the spec is not expected to describe.
func undocumented(method, path string) bool {
	switch {
	// The SPA fallback and static assets: not an API.
	case path == "/*", path == "/":
		return true
	// CalDAV speaks PROPFIND and REPORT, which OpenAPI has no way to describe.
	// The discovery URL is in the spec; the rest is documented in docs/caldav.md.
	case strings.HasPrefix(path, "/caldav"):
		return true
	// chi registers OPTIONS and HEAD of its own accord in places; neither carries
	// behaviour worth writing down.
	case method == http.MethodOptions || method == http.MethodHead:
		return true
	}
	return false
}

func TestOpenAPICoversEveryRoute(t *testing.T) {
	spec := specRoutes(t)

	// The server itself rather than the test harness's httptest wrapper: this
	// needs the router, and nothing here sends a request.
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(&config.Config{
		BaseURL: "http://127.0.0.1", DataDir: t.TempDir(),
		SessionTTL: time.Hour, InviteTTL: time.Hour, ResetTTL: time.Hour,
	}, db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	routed := map[string]bool{}
	err = chi.Walk(srv.router,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			// chi writes the index of a sub-router as a trailing slash; the spec
			// names it without one.
			if route != "/" {
				route = strings.TrimSuffix(route, "/")
			}
			if !undocumented(method, route) {
				routed[method+" "+route] = true
			}
			return nil
		})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	var missing, stale []string
	for route := range routed {
		if !spec[route] {
			missing = append(missing, route)
		}
	}
	for route := range spec {
		if !routed[route] {
			stale = append(stale, route)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)

	for _, route := range missing {
		t.Errorf("routed but not in docs/openapi.yaml: %s", route)
	}
	for _, route := range stale {
		t.Errorf("in docs/openapi.yaml but not routed: %s", route)
	}
}
