package store

import (
	"context"
	"time"
)

// The collector's side of the beacon: what has reported in, and how many.
//
// Every method here is about counting installations, never about identifying one.
// There is no lookup by anything but the id an installation made up for itself,
// and there is nothing to join it against.

// BeaconStats is the whole of what the collector can say.
type BeaconStats struct {
	Total      int            `json:"total"`
	Active7d   int            `json:"active_7d"`
	Active30d  int            `json:"active_30d"`
	ByVersion  map[string]int `json:"by_version"`
	LastPingAt *time.Time     `json:"last_ping_at,omitempty"`
}

// maxBeaconInstalls bounds the table.
//
// The receiving endpoint is unauthenticated — it has to be, since the caller is a
// stranger's installation — and it inserts a row keyed on an id the caller made
// up. Anybody can therefore mint ids and grow this table until the volume the
// database lives on is full. A ceiling turns that from a disk-fill into a number
// that is merely wrong, which is the difference between an outage and an
// annoyance. A hundred thousand is far above any plausible real count and far
// below anything that matters on disk.
const maxBeaconInstalls = 100_000

// RecordBeacon writes down that an installation is alive, and on which version.
//
// `first_seen` is kept from the original row on purpose: it is the only thing
// that distinguishes an installation that has been running for a year from one
// that appeared this morning, and it costs nothing to keep.
func (db *DB) RecordBeacon(ctx context.Context, instanceID, version string) error {
	now := time.Now().Unix()

	// An installation already known keeps reporting whatever the ceiling says: the
	// cap must never stop counting the real ones, only stop new rows from being
	// invented without limit.
	var known int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM beacon_installs WHERE instance_id = ?`, instanceID).Scan(&known); err != nil {
		return err
	}
	if known == 0 {
		var total int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM beacon_installs`).Scan(&total); err != nil {
			return err
		}
		if total >= maxBeaconInstalls {
			// Not an error to the caller. Their installation did nothing wrong, and
			// there is nothing for them to do about it.
			return nil
		}
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO beacon_installs (instance_id, version, first_seen, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (instance_id) DO UPDATE SET
		    version   = excluded.version,
		    last_seen = excluded.last_seen`,
		instanceID, version, now, now)
	return err
}

// BeaconCount is how many installations have reported within the window.
//
// A window rather than a running total, and that is not a detail. A total only
// ever climbs: every instance anybody ever started for ten minutes stays in it
// forever, so the number stops meaning "how many people use this" and starts
// meaning "how many people have ever tried it". The question being asked is the
// first one.
func (db *DB) BeaconCount(ctx context.Context, within time.Duration) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM beacon_installs WHERE last_seen >= ?`,
		time.Now().Add(-within).Unix()).Scan(&n)
	return n, err
}

// BeaconSummary is everything the settings page shows.
func (db *DB) BeaconSummary(ctx context.Context) (BeaconStats, error) {
	out := BeaconStats{ByVersion: map[string]int{}}

	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM beacon_installs`).Scan(&out.Total); err != nil {
		return out, err
	}

	var err error
	if out.Active7d, err = db.BeaconCount(ctx, 7*24*time.Hour); err != nil {
		return out, err
	}
	if out.Active30d, err = db.BeaconCount(ctx, 30*24*time.Hour); err != nil {
		return out, err
	}

	// Versions of the ones still alive, not of everything ever seen: an instance
	// that stopped reporting two years ago should not still be counted as running
	// the version it was on when it stopped.
	rows, err := db.QueryContext(ctx, `
		SELECT version, count(*) FROM beacon_installs
		 WHERE last_seen >= ? AND version <> ''
		 GROUP BY version`, time.Now().Add(-30*24*time.Hour).Unix())
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		var n int
		if err := rows.Scan(&v, &n); err != nil {
			return out, err
		}
		out.ByVersion[v] = n
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	var last *int64
	if err := db.QueryRowContext(ctx,
		`SELECT max(last_seen) FROM beacon_installs`).Scan(&last); err != nil {
		return out, err
	}
	if last != nil {
		t := time.Unix(*last, 0)
		out.LastPingAt = &t
	}
	return out, nil
}
