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

// defaultPublishMin er, hvor mange installationer der skal være, før tallet
// offentliggøres. Selve tallet er et skøn; det, gulvet køber, er det ikke: under
// det fortæller et offentligt tal mest om, hvor lidt der skal til at flytte det.
const defaultPublishMin = 25

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
	// LastVersion er den version, der sidst nåede frem. Gemt, fordi en
	// installation, der opdaterer sig selv om natten, ellers står som gårsdagens
	// version i op til et døgn — og en tælling efter version, der er en dag bagud,
	// kan man ikke se en udrulning i.
	LastVersion string `json:"last_version,omitempty"`
	// LastError er grunden til, at det sidste forsøg ikke nåede frem.
	//
	// Skrevet ned, fordi alternativet er tavshed: et beacon, der ikke kan nå sin
	// collector, prøver igen i morgen og siger ingenting, og det opdages først ved
	// at tælle installationer i den anden ende og savne en. Det er ikke en fejl,
	// der skal afbryde nogen — det er en linje på en side, der ellers påstår, at
	// alt er sendt.
	LastError   string `json:"last_error,omitempty"`
	LastErrorAt int64  `json:"last_error_at,omitempty"`
	// NoticeAck er sat, når nogen har set beskeden om, hvad der bliver sendt.
	//
	// Slået til som udgangspunkt kan kun forsvares, hvis det bliver sagt
	// uopfordret — ikke på en indstillingsside, ingen åbner af sig selv. Feltet er
	// det, der husker, at det er blevet sagt.
	NoticeAck bool `json:"notice_ack"`
	// PublishMin er gulvet under det offentliggjorte tal.
	//
	// Et tal, der *falder*, kan ikke offentliggøres som en kendsgerning: en dårlig
	// udrulning, en DNS-ændring, der stille stopper meldingerne, eller rækker, der
	// er væk, ser alle sammen ud som "færre bruger det nu". Under gulvet svares
	// der ingenting frem for et lille tal, ingen kan tolke.
	PublishMin int `json:"publish_min"`
}

// beaconSettings reads the configuration, filling in the defaults and minting the
// anonymous id on first use.
//
// The id is generated here rather than derived from anything about the machine.
// A hash of the hostname or the base URL would be stable and convenient and would
// also be a value the collector could compare against a guess — which is the
// difference between anonymous and pseudonymous, and it is the whole point.
func (s *Server) beaconSettings(ctx context.Context) (beaconConfig, error) {
	cfg := beaconConfig{
		Enabled: true, Collector: beacon.DefaultCollector, PublishMin: defaultPublishMin,
	}

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
	if v, ok := stored["last_version"].(string); ok {
		cfg.LastVersion = v
	}
	if v, ok := stored["last_error"].(string); ok {
		cfg.LastError = v
	}
	if v, ok := stored["last_error_at"].(float64); ok {
		cfg.LastErrorAt = int64(v)
	}
	if v, ok := stored["notice_ack"].(bool); ok {
		cfg.NoticeAck = v
	}
	if v, ok := stored["publish_min"].(float64); ok && v >= 0 {
		cfg.PublishMin = int(v)
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
		"last_version":  cfg.LastVersion,
		"last_error":    cfg.LastError,
		"last_error_at": cfg.LastErrorAt,
		"notice_ack":    cfg.NoticeAck,
		"publish_min":   cfg.PublishMin,
	})
}

// --- what the settings page shows ------------------------------------------------

type beaconStatusJSON struct {
	Enabled      bool   `json:"enabled"`
	Collector    string `json:"collector_url"`
	InstanceID   string `json:"instance_id"`
	Version      string `json:"version"`
	IsCollector  bool   `json:"is_collector"`
	PublishCount bool   `json:"publish_count"`
	PublishMin   int    `json:"publish_min"`
	LastPingAt   string `json:"last_ping_at,omitempty"`
	// Sidste forsøg, der ikke nåede frem — tomt, når det sidste gik godt. Vist
	// frem for talt: en dato under "sidst sendt", der bliver ældre og ældre, er
	// det eneste spor, et tavst beacon ellers efterlader.
	LastError   string             `json:"last_error,omitempty"`
	LastErrorAt string             `json:"last_error_at,omitempty"`
	Stats       *store.BeaconStats `json:"stats,omitempty"`
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
		PublishMin: cfg.PublishMin,
	}
	if cfg.LastPingAt > 0 {
		out.LastPingAt = time.Unix(cfg.LastPingAt, 0).Format(time.RFC3339)
	}
	if cfg.LastError != "" {
		out.LastError = cfg.LastError
		out.LastErrorAt = time.Unix(cfg.LastErrorAt, 0).Format(time.RFC3339)
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
	PublishMin   *int    `json:"publish_min"`
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
	if req.PublishMin != nil {
		if *req.PublishMin < 0 {
			writeFieldErrors(w, map[string]string{"publish_min": "must not be negative"})
			return
		}
		cfg.PublishMin = *req.PublishMin
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
	if err == nil && n < cfg.PublishMin {
		// Ingenting frem for et lille tal. Et tal under gulvet fortæller mest om,
		// hvor lidt der skal til at flytte det — og et, der falder, ville blive
		// læst som "færre bruger det nu", også når det i virkeligheden var en
		// udrulning, der gik galt, eller en adresse, der stille holdt op med at
		// blive ramt.
		w.Header().Set("Cache-Control", "public, max-age=60")
		writeJSON(w, http.StatusOK, map[string]any{"installs": nil, "window_days": 30})
		return
	}
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
		return s.beaconSent(ctx, cfg)
	}

	// Undtagelsen fra døgnet er en version, der er skiftet. En installation, der
	// opdaterer sig selv om natten og først melder ind igen et døgn senere, står
	// som gårsdagens version hele dagen — og så kan man ikke se en udrulning i
	// tallene. Målt hos Yggdrasil: to paneler rapporterede den forrige version i
	// et døgn efter at være opdateret.
	if time.Since(time.Unix(cfg.LastPingAt, 0)) < 24*time.Hour && cfg.LastVersion == Version {
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
		// Men skrevet ned. Uden det her fejler et beacon, der ikke kan nå sin
		// collector, i fuldstændig tavshed: det prøver igen i morgen, siden siger
		// "sidst sendt" med en dato, der bare bliver ældre, og ingen opdager det.
		// Fundet hos Yggdrasil ved at tælle installationer i den anden ende og
		// savne en.
		cfg.LastError, cfg.LastErrorAt = err.Error(), time.Now().Unix()
		return s.saveBeaconSettings(ctx, cfg)
	}

	return s.beaconSent(ctx, cfg)
}

// beaconSent skriver, at det lykkedes: tidspunktet, versionen der nåede frem, og
// en fejl, der ikke længere gælder. Ét sted, fordi de fire felter skal skrives
// sammen — en gemning, der glemte at rydde fejlen, ville lade en linje stå på
// siden om noget, der virker.
func (s *Server) beaconSent(ctx context.Context, cfg beaconConfig) error {
	cfg.LastPingAt = time.Now().Unix()
	cfg.LastVersion = Version
	cfg.LastError, cfg.LastErrorAt = "", 0
	return s.saveBeaconSettings(ctx, cfg)
}

// --- beskeden, der bliver sagt uopfordret ------------------------------------------

// handleBeaconNotice says whether the disclosure is still owed, and what it would
// say.
//
// Slået til som udgangspunkt kan kun forsvares, hvis det bliver sagt uopfordret.
// En indstillingsside, hvor det står rigtigt og fyldestgørende, er ikke det samme
// som at sige det: den bliver åbnet af dem, der i forvejen har en mistanke om, at
// der er noget at læse. Uden beskeden her er "du kan slå den fra" et tilbud til
// dem, der ved, at der er noget at slå fra.
//
// De tre værdier står i svaret — det rigtige id, den rigtige version, den rigtige
// adresse — så beskeden kan vise, hvad der bliver sendt, frem for at sige
// "anonyme data" og bede om at blive troet på. Det er den eneste version af det
// løfte, der er noget værd: en, læseren selv kan kontrollere.
//
// Panelbredt frem for pr. bruger: det er installationen, der melder ind, ikke
// personen, og en besked pr. konto ville stille det samme spørgsmål til folk, der
// ikke kan svare på det.
func (s *Server) handleBeaconNotice(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.beaconSettings(r.Context())
	if err != nil {
		s.internal(w, r, "beacon settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending":       !cfg.NoticeAck,
		"enabled":       cfg.Enabled,
		"instance_id":   cfg.InstanceID,
		"version":       Version,
		"collector_url": cfg.Collector,
	})
}

type beaconNoticeRequest struct {
	// Keep er en peger og ikke en bool, og det er hele pointen.
	//
	// En tom krop ville ellers betyde "slå det fra", og det er den vej, en fejl
	// aldrig må falde: et fravalg, ingen har truffet, er lige så meget et brudt
	// løfte som et tilvalg, ingen har truffet. Yggdrasil har den fejl i sin
	// tilsvarende ende og er blevet bidt af den.
	Keep *bool `json:"keep"`
}

// handleAckBeaconNotice records that the notice has been seen, and turns the
// beacon off if that was the answer.
//
// Ét kald til begge svar. "Behold den" og "slå den fra" er den samme handling —
// nogen har taget stilling — og to endpoints ville betyde, at det ene kunne
// glemme at skrive, at der var taget stilling, og beskeden dermed komme igen.
func (s *Server) handleAckBeaconNotice(w http.ResponseWriter, r *http.Request) {
	var req beaconNoticeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if req.Keep == nil {
		writeFieldErrors(w, map[string]string{"keep": "required"})
		return
	}

	cfg, err := s.beaconSettings(r.Context())
	if err != nil {
		s.internal(w, r, "beacon settings", err)
		return
	}
	cfg.NoticeAck = true
	cfg.Enabled = *req.Keep
	if err := s.saveBeaconSettings(r.Context(), cfg); err != nil {
		s.internal(w, r, "save beacon settings", err)
		return
	}
	s.log.Info("beacon notice answered", "keep", *req.Keep, "user", userFrom(r.Context()).ID)
	writeJSON(w, http.StatusOK, map[string]any{"enabled": cfg.Enabled})
}
