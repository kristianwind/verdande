package httpapi

import (
	"net/http"
	"testing"
)

// The backup panel exists because the job was invisible, so the thing to check is
// that it can be seen and asked for — and that the file it hands out is guarded
// like what it is, which is every account's data in one download.
func TestABackupCanBeTakenListedAndDownloaded(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)

	// Nothing yet, and that is a state the list has to survive rather than a case
	// nobody hits: it is what a fresh instance looks like.
	_, empty := ts.do(t, "GET", "/api/v1/backups", nil)
	if got, _ := empty["backups"].([]any); len(got) != 0 {
		t.Fatalf("a fresh instance has %d backups", len(got))
	}

	resp, made := ts.do(t, "POST", "/api/v1/backups", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("run backup: status %d, body %v", resp.StatusCode, made)
	}
	id, _ := made["id"].(string)
	if id == "" {
		t.Fatal("the new run has no id")
	}
	if size, _ := made["size_bytes"].(float64); size <= 0 {
		t.Errorf("size_bytes = %v — a backup of a database with rows in it is not empty", made["size_bytes"])
	}
	if made["error"] != nil {
		t.Errorf("the backup reported an error: %v", made["error"])
	}

	_, listed := ts.do(t, "GET", "/api/v1/backups", nil)
	rows, _ := listed["backups"].([]any)
	if len(rows) != 1 {
		t.Fatalf("listed %d backups, want 1", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["id"] != id {
		t.Errorf("the listed run is not the one that was made")
	}
	if row["present"] != true {
		t.Error("present = false on a backup that was just written")
	}

	// And the file itself, which must arrive as a download rather than as
	// something a browser might try to render.
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/backups/"+id, nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	dl, err := ts.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("download: status %d", dl.StatusCode)
	}
	if ct := dl.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	if cd := dl.Header.Get("Content-Disposition"); cd == "" || cd[:10] != "attachment" {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}

	// An id that is not a backup is a 404, not a path to try opening.
	if resp, _ := ts.do(t, "GET", "/api/v1/backups/../../etc/passwd", nil); resp.StatusCode == http.StatusOK {
		t.Error("a traversal in the id was served")
	}
	if resp, _ := ts.do(t, "GET", "/api/v1/backups/nogetsomhelst", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("an unknown id: status %d, want 404", resp.StatusCode)
	}
}

// Administrators only, and sessions only. A backup is a complete copy of the
// database — a leaked token that could download one turns a stolen credential into
// a copy of everybody's data.
func TestBackupsAreAdminsOnlyAndSessionsOnly(t *testing.T) {
	ts := newTestServer(t)
	ts.bootstrap(t)
	other := ts.newUser(t, "anden@example.dk", "Anden")

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/backups"},
		{"POST", "/api/v1/backups"},
		{"GET", "/api/v1/backups/whichever"},
	} {
		if resp, _ := other.do(t, c.method, c.path, nil); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s as an ordinary user: status %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}

	admin, err := ts.db.UserByEmail(t.Context(), "kristian@example.dk")
	if err != nil {
		t.Fatal(err)
	}
	token := ts.apiToken(t, admin.ID)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/backups"},
		{"POST", "/api/v1/backups"},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with an admin's API token: status %d, want 403", c.method, c.path, resp.StatusCode)
		}
	}
}
