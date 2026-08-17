// Package update checks whether a newer verdande has been released.
//
// It asks GitHub's public releases API and nothing else. No identifier is sent, no
// telemetry is collected, and the request carries only a user agent naming the
// project — a self-hosted app that phones home with anything about its instance has
// broken the deal its operator made by self-hosting.
//
// The check is off unless the operator turns it on, and the result is cached for
// six hours so a busy instance does not become a burst of requests to GitHub.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const releasesURL = "https://api.github.com/repos/kristianwind/verdande/releases/latest"

// Status is what the interface shows.
type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"update_available"`
	URL       string `json:"url,omitempty"`
	// Notes is the release's own description, so somebody can decide whether the
	// update is worth a restart tonight or next week.
	Notes     string `json:"notes,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
	// Disabled distinguishes "no update" from "we never looked", which the
	// interface has to show differently or it implies a guarantee it cannot make.
	Disabled bool `json:"disabled"`
}

type Checker struct {
	current string
	enabled bool

	mu      sync.RWMutex
	cached  Status
	fetched time.Time
}

func New(currentVersion string, enabled bool) *Checker {
	return &Checker{
		current: currentVersion,
		enabled: enabled,
		cached:  Status{Current: currentVersion, Disabled: !enabled},
	}
}

// Status returns what is known, refreshing at most every six hours.
func (c *Checker) Status(ctx context.Context) Status {
	if !c.enabled {
		return Status{Current: c.current, Disabled: true}
	}

	c.mu.RLock()
	cached, fetched := c.cached, c.fetched
	c.mu.RUnlock()

	if time.Since(fetched) < 6*time.Hour && fetched != (time.Time{}) {
		return cached
	}

	status, err := c.fetch(ctx)
	if err != nil {
		// A failed check is not an error anybody needs to see. GitHub being
		// briefly unreachable is not a problem with this instance, and the last
		// known answer is better than a red banner.
		c.mu.Lock()
		// Backed off by marking it fetched, so a network outage does not mean a
		// request to GitHub on every page load.
		c.fetched = time.Now()
		result := c.cached
		c.mu.Unlock()
		return result
	}

	c.mu.Lock()
	c.cached, c.fetched = status, time.Now()
	c.mu.Unlock()
	return status
}

func (c *Checker) fetch(ctx context.Context) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return Status{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// The only thing sent about this instance is the project name. Not the base
	// URL, not the version, not a count of anything.
	req.Header.Set("User-Agent", "verdande")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("update: github said %s", resp.Status)
	}

	var release struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Body       string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return Status{}, err
	}
	if release.Draft || release.Prerelease {
		return Status{Current: c.current, CheckedAt: nowRFC3339()}, nil
	}

	return Status{
		Current:   c.current,
		Latest:    release.TagName,
		Available: IsNewer(c.current, release.TagName),
		URL:       release.HTMLURL,
		Notes:     truncate(release.Body, 2000),
		CheckedAt: nowRFC3339(),
	}, nil
}

// IsNewer compares two semver tags.
//
// A development build never reports an update. Somebody running `go run` against
// their own working tree is ahead of every release, not behind it, and telling them
// otherwise every time they open the app would train them to ignore the notice.
func IsNewer(current, latest string) bool {
	if current == "" || latest == "" {
		return false
	}
	if current == "dev" || strings.Contains(current, "-dirty") {
		return false
	}

	c := parseVersion(current)
	l := parseVersion(latest)
	if c == nil || l == nil {
		return false
	}

	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parseVersion reads "v1.2.3" into its three numbers, ignoring any pre-release or
// build suffix. Returns nil for anything that is not a version.
func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	// Drop a pre-release or build suffix: 1.2.3-rc1 compares as 1.2.3, which is
	// close enough for "is there something newer" and avoids implementing the
	// whole precedence section of the semver spec for a notice in a corner.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
