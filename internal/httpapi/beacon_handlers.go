package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/kristianwind/verdande/internal/beacon"
	"github.com/kristianwind/verdande/internal/safedial"
	"github.com/kristianwind/verdande/internal/store"
)

// The beacon, from the instance's side.
//
// Two halves that happen to live in the same binary: the part that reports in,
// which every installation runs, and the part that receives, which only the one
// acting as collector turns on. See internal/beacon for what is sent and why it
// is on by default rather than compulsory.

const beaconScope = "beacon"

// beaconConfig is what this instance has been told to do. The zero value is what
// a fresh installation gets, which is why Enabled defaults to true elsewhere and
// not here — a zero struct must never mean "silently on".
type beaconConfig struct {
	Enabled      bool   `json:"enabled"`
	Collector    string `json:"collector_url"`
	InstanceID   string `json:"instance_id"`
	IsCollector  bool   `json:"is_collector"`
	PublishCount bool   `json:"publish_count"`
	LastPingAt   int64  `json:"last_ping_at,omitempty"`
}

// beaconSettings reads the configuration, filling in the defaults and minting the
// anonymous id on first use.
//
// The id is generated here rather than derived from anything about the machine.
// A hash of the hostname or the base URL would be stable and convenient and would
// also be a value the collector could compare against a guess — which is the
// difference between anonymous and pseudonymous, and it is the whole point.
func (s *Server) beaconSettings(ctx context.Context) (beaconConfig, error) {
	cfg := beaconConfig{Enabled: true, Collector: beacon.DefaultCollector}

	stored, err := s.db.InstanceSettings(ctx, beaconScope)
	if err != nil {
		return cfg, err
	}

	if v, ok := stored["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v, ok := stored["collector_url"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.Collector = strings.TrimSpace(v)
	}
	if v, ok := stored["instance_id"].(string); ok && beacon.ValidID(v) {
		cfg.InstanceID = v
	}
	if v, ok := stored["is_collector"].(bool); ok {
		cfg.IsCollector = v
	}
	if v, ok := stored["publish_count"].(bool); ok {
		cfg.PublishCount = v
	}
	if v, ok := stored["last_ping_at"].(float64); ok {
		cfg.LastPingAt = int64(v)
	}

	if cfg.InstanceID == "" {
		cfg.InstanceID = store.NewID()
		if err := s.saveBeaconSettings(ctx, cfg); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}

func (s *Server) saveBeaconSettings(ctx context.Context, cfg beaconConfig) error {
	return s.db.SetInstanceSettings(ctx, beaconScope, map[string]any{
		"enabled":       cfg.Enabled,
		"collector_url": cfg.Collector,
		"instance_id":   cfg.InstanceID,
		"is_collector":  cfg.IsCollector,
		"publish_count": cfg.PublishCount,
		"last_ping_at":  cfg.LastPingAt,
	})
}

// --- what the settings page shows ------------------------------------------------

type beaconStatusJSON struct {
	Enabled      bool               `json:"enabled"`
	Collector    string             `json:"collector_url"`
	InstanceID   string             `json:"instance_id"`
	Version      string             `json:"version"`
	IsCollector  bool               `json:"is_collector"`
	PublishCount bool               `json:"publish_count"`
	LastPingAt   string             `json:"last_ping_at,omitempty"`
	Stats        *store.BeaconStats `json:"stats,omitempty"`
}

func (s *Server) handleBeaconStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.beaconSettings(r.Context())
	if err != nil {
		s.internal(w, r, "beacon settings", err)
		return
	}

	out := beaconStatusJSON{
		Enabled: cfg.Enabled, Collector: cfg.Collector, InstanceID: cfg.InstanceID,
		Version: Version, IsCollector: cfg.IsCollector, PublishCount: cfg.PublishCount,
	}
	if cfg.LastPingAt > 0 {
		out.LastPingAt = time.Unix(cfg.LastPingAt, 0).Format(time.RFC3339)
	}
	if cfg.IsCollector {
		stats, err := s.db.BeaconSummary(r.Context())
		if err != nil {
			s.internal(w, r, "beacon summary", err)
			return
		}
		out.Stats = &stats
	}
	writeJSON(w, http.StatusOK, out)
}

type beaconRequest struct {
	Enabled      *bool   `json:"enabled"`
	Collector    *string `json:"collector_url"`
	IsCollector  *bool   `json:"is_collector"`
	PublishCount *bool   `json:"publish_count"`
}

func (s *Server) handleSetBeacon(w http.ResponseWriter, r *http.Request) {
	var req beaconRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	cfg, err := s.beaconSettings(r.Context())
	if err != nil {
		s.internal(w, r, "beacon settings", err)
		return
	}

	if req.Enabled != nil {
		cfg.Enabled = *req.Enabled
	}
	if req.IsCollector != nil {
		cfg.IsCollector = *req.IsCollector
	}
	if req.PublishCount != nil {
		cfg.PublishCount = *req.PublishCount
	}
	if req.Collector != nil {
		url := strings.TrimSpace(*req.Collector)
		if url == "" {
			url = beacon.DefaultCollector
		}
		// http and https only, and said as a field error rather than a 500: an
		// operator pointing this at their own collector will mistype it, and the
		// page should say which field is wrong.
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			writeFieldErrors(w, map[string]string{"collector_url": "must start with http:// or https://"})
			return
		}
		cfg.Collector = url
	}

	if err := s.saveBeaconSettings(r.Context(), cfg); err != nil {
		s.internal(w, r, "save beacon settings", err)
		return
	}
	s.handleBeaconStatus(w, r)
}

// --- the collector's side ---------------------------------------------------------

// handleBeaconPing receives one report. Unauthenticated, because the whole point
// is that a stranger's installation can reach it.
//
// Nothing about the request is recorded except the two values in the body. The
// source address is not read here, and there is no code path from this function
// to anything that could write one down — which is the only version of that
// promise worth making.
func (s *Server) handleBeaconPing(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.beaconSettings(r.Context())
	if err != nil {
		s.internal(w, r, "beacon settings", err)
		return
	}
	// An instance that is not a collector does not quietly accept and drop pings —
	// it says there is nothing here. Otherwise every installation in the world is a
	// spam sink that answers 200 to anything.
	if !cfg.IsCollector {
		writeError(w, http.StatusNotFound, CodeNotFound, "this instance is not a beacon collector")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var p beacon.Payload
	if err := decodeJSON(w, r, &p); err != nil {
		return
	}

	// Checked, not trusted. This is the one endpoint anybody can post to, and the
	// two columns behind it should only ever hold the two shapes this program
	// writes — an id that is not id-shaped is somebody probing, not an install.
	if !beacon.ValidID(p.InstanceID) {
		writeFieldErrors(w, map[string]string{"instance_id": "malformed"})
		return
	}
	if p.Version != "" && !beacon.ValidVersion(p.Version) {
		writeFieldErrors(w, map[string]string{"version": "malformed"})
		return
	}

	if err := s.db.RecordBeacon(r.Context(), p.InstanceID, p.Version); err != nil {
		s.internal(w, r, "record beacon", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBeaconCount publishes the number, when the collector has been told to.
//
// Thirty days rather than a running total: a total only climbs, so it stops
// meaning "how many people use this" and starts meaning "how many ever tried it".
func (s *Server) handleBeaconCount(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.beaconSettings(r.Context())
	if err != nil {
		s.internal(w, r, "beacon settings", err)
		return
	}
	if !cfg.IsCollector || !cfg.PublishCount {
		writeError(w, http.StatusNotFound, CodeNotFound, "not published")
		return
	}

	n, err := s.db.BeaconCount(r.Context(), 30*24*time.Hour)
	if err != nil {
		s.internal(w, r, "beacon count", err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, http.StatusOK, map[string]any{"installs": n, "window_days": 30})
}

// SendBeacon reports this installation, at most once a day.
//
// Exported and handed to the jobs runner the same way the mailbox syncs are: the
// settings, the anonymous id and the version all live on this side, and the
// runner should not have to know about any of them.
//
// The day is counted from the last successful send, not from a wall-clock hour.
// A fleet of installations that all pinged at 03:00 would arrive in one spike;
// counted this way they spread themselves out by when each was started.
func (s *Server) SendBeacon(ctx context.Context) error {
	cfg, err := s.beaconSettings(ctx)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	// The collector does not report to itself over the network. It would work, and
	// it would also mean the one instance whose count matters most depends on its
	// own tunnel being up to be counted.
	if cfg.IsCollector {
		if err := s.db.RecordBeacon(ctx, cfg.InstanceID, Version); err != nil {
			return err
		}
		cfg.LastPingAt = time.Now().Unix()
		return s.saveBeaconSettings(ctx, cfg)
	}

	if time.Since(time.Unix(cfg.LastPingAt, 0)) < 24*time.Hour {
		return nil
	}

	// safedial, ikke http.DefaultClient. Adressen kan skrives i fladen, og en
	// administrator, der peger den på 127.0.0.1 eller på 169.254.169.254, ville
	// ellers have en maskine, der henter indefra på kommando — og svaret på om den
	// svarede, er i sig selv det, sådan et forsøg leder efter.
	if err := beacon.Send(ctx, safedial.Client(15*time.Second), cfg.Collector, beacon.Payload{
		InstanceID: cfg.InstanceID,
		Version:    Version,
	}); err != nil {
		// Logged at info, not error. The collector being unreachable is not this
		// installation's problem and must never look like one — it is somebody
		// else's server, and the only consequence is a number being one lower.
		s.log.Info("beacon not delivered", "err", err)
		return nil
	}

	cfg.LastPingAt = time.Now().Unix()
	return s.saveBeaconSettings(ctx, cfg)
}
