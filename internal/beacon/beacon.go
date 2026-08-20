// Package beacon answers one question for the project, and refuses to answer any
// other: how many installations are out there.
//
// It sends two values once a day — an anonymous id the installation made up for
// itself, and the version it is running — and nothing else. No IP is stored by
// the collector, no hostname, no domain, no account, no usage. The payload is
// small enough to be printed in full on the settings page, and it is, because a
// promise about telemetry is worth exactly as much as the reader's ability to
// check it.
//
// # On by default, and off in one click
//
// The alternative that was asked for first was mandatory, and the argument
// against it is practical rather than principled: it cannot be enforced. The
// domain can be blocked, the binary can be patched, and a fork removes it in its
// first commit — so mandatory buys nothing over on-by-default, while costing the
// trust the rest of this program works for. Secrets are sealed here, the key is
// kept out of the database, and the backup page warns you what a copy contains.
// Telemetry you are not allowed to refuse is the one sentence in that story that
// does not fit.
//
// So: on by default, said plainly, one switch to turn it off. That collects
// essentially the same number and leaves nobody feeling tricked.
//
// # Where this disagrees with the rest of the program
//
// `config.UpdateCheck` is off unless asked for, and its comment gives the reason:
// "a self-hosted app that reaches out without being told to has broken the deal
// its operator made by self-hosting." That is the same situation as this one, and
// the two defaults do not agree.
//
// The disagreement is written down here rather than smoothed over, because it is
// a real one and the reader deserves to see both halves. The case for treating
// them differently is that the update check reveals an instance to a third party
// (GitHub) and returns something the operator can act on, while this returns
// nothing and goes to the project itself — and that a count nobody sends is a
// count that does not exist, whereas an update nobody checks for is one you can
// still go and look up. Whether that is a distinction or an excuse is a judgement
// call, and it was made deliberately by the person whose instance this is.
//
// If it is ever decided the other way, the change is one word: the default in
// `beaconSettings`. Nothing else in this package assumes which way it points.
//
// # What the collector can still see
//
// A request has a source address whether or not anybody writes it down. The
// collector does not store or log it, and the code is arranged so that it never
// reaches a function that could — but it passes through a reverse proxy and a
// tunnel on the way, and those keep their own logs. Saying "we send no IP" would
// be a half-truth; what is true is that nothing here records one.
package beacon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// DefaultCollector is where a fresh installation reports, unless it is told
// otherwise. Somebody running their own fleet points it at their own instance;
// the field is on the settings page for exactly that.
const DefaultCollector = "https://verdande.yggdrasilpanel.com/api/v1/beacon"

// Payload is the entire message. Two fields, and adding a third is a change to
// what this program promises, not a change to a struct.
type Payload struct {
	InstanceID string `json:"instance_id"`
	Version    string `json:"version"`
}

// InstanceID and Version are checked on the way in as well as written on the way
// out, because the collector's endpoint is unauthenticated: anything that reaches
// it is a stranger's bytes, and it should be able to store only the two shapes it
// expects.
var (
	idShape      = regexp.MustCompile(`^[0-9a-fA-F-]{16,64}$`)
	versionShape = regexp.MustCompile(`^v?[0-9]{1,4}(\.[0-9]{1,5}){0,3}(-[0-9A-Za-z.]{1,20})?$`)
)

// ValidID reports whether an id is the shape this program writes.
func ValidID(s string) bool { return idShape.MatchString(s) }

// ValidVersion allows a version number and the word this binary uses when it was
// built outside a release.
func ValidVersion(s string) bool { return s == "dev" || versionShape.MatchString(s) }

// Send delivers one ping. It is deliberately dull: a short timeout, no retry, no
// queue, and a failure that is returned rather than made anybody's problem.
//
// No retry, because the next pass is tomorrow and a missed day changes a count of
// installations by nothing. A telemetry ping that retries is a telemetry ping
// that can hammer somebody's server on the day it is down.
func Send(ctx context.Context, client *http.Client, collector string, p Payload) error {
	if !ValidID(p.InstanceID) {
		return fmt.Errorf("beacon: refusing to send a malformed id")
	}
	// Versionen kontrolleres også her, ikke kun hos modtageren. Kommentaren ved
	// mønstrene lovede begge veje, og gjorde det ikke — og en installation, der
	// sender noget mærkeligt, skal opdage det hos sig selv frem for at være noget,
	// en fremmed collector skal filtrere fra.
	if !ValidVersion(p.Version) {
		return fmt.Errorf("beacon: refusing to send a malformed version")
	}
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, collector, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// A plain user agent with the version already in the payload. Nothing is
	// learned from it that the body does not already say, and it makes the request
	// legible in a log rather than anonymous-looking.
	req.Header.Set("User-Agent", "verdande-beacon/1")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Read and discard, so the connection can be reused rather than dropped.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	if resp.StatusCode >= 400 {
		return fmt.Errorf("beacon: collector answered %d", resp.StatusCode)
	}
	return nil
}
