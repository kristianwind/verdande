package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Asking the panel to restart this instance.
//
// A container cannot replace its own image from the inside, so "update" is really
// "ask whoever owns the container to recreate it" — Yggdrasil pulls `:latest` on
// the way, which is what makes a restart an update at all.
//
// The alternative was a second browser tab: read the version here, go to the
// panel, find the server, press Restart. This removes the tab, not the panel's
// authority — the panel still does the work and still records who asked.
//
// Administrators only, and sessions only. A leaked API token that could restart
// the server is a denial of service in one request, and the whole point of the
// session rule elsewhere in this file is that a stolen token must not be able to
// do things only a person at a keyboard should.

type panelStatusJSON struct {
	// Configured is all three settings being present. The token itself is never
	// sent: the page needs to know whether the button can work, not what the
	// credential is.
	Configured bool   `json:"configured"`
	PanelURL   string `json:"panel_url,omitempty"`
	// Why it cannot work, when it cannot. An operator reading "not configured"
	// should not have to go and find out which of three things is missing.
	Missing []string `json:"missing,omitempty"`
}

func (s *Server) panelStatus() panelStatusJSON {
	var missing []string
	if s.cfg.PanelURL == "" {
		missing = append(missing, "VERDANDE_PANEL_URL")
	}
	if s.cfg.PanelToken == "" {
		missing = append(missing, "VERDANDE_PANEL_TOKEN")
	}
	if s.cfg.PanelServerID == "" {
		missing = append(missing, "VERDANDE_PANEL_SERVER_ID")
	}
	return panelStatusJSON{
		Configured: len(missing) == 0,
		PanelURL:   s.cfg.PanelURL,
		Missing:    missing,
	}
}

func (s *Server) handlePanelStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.panelStatus())
}

func (s *Server) handleRestartFromPanel(w http.ResponseWriter, r *http.Request) {
	status := s.panelStatus()
	if !status.Configured {
		writeError(w, http.StatusServiceUnavailable, CodeInternal,
			fmt.Sprintf("this instance cannot restart itself: %v are not set", status.Missing))
		return
	}

	user := userFrom(r.Context())
	s.log.Info("restart requested from the interface", "user", user.ID, "server", s.cfg.PanelServerID)

	// A short timeout of its own. The panel stops this container as part of
	// answering, so the reply may never arrive — that is not a failure, and a
	// request that hung waiting for it would look like one.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()

	// safe-restart rather than restart, and the difference is not politeness.
	//
	// The plain endpoint does its work while the request is open: it stops the
	// container — which kills this process — and the panel, seeing the caller
	// vanish, never gets to the starting half. That took production down and left
	// it down for four minutes on 18 August, and it cannot be fixed from this side:
	// any button that synchronously asks for its own death has the same shape.
	//
	// safe-restart is scheduled. The panel answers "Restart scheduled" and does the
	// work on its own afterwards, so nothing depends on this process still being
	// alive to hear the reply.
	url := fmt.Sprintf("%s/api/servers/%s/safe-restart", s.cfg.PanelURL, s.cfg.PanelServerID)
	body := strings.NewReader(`{"backup_first":false,"target_id":""}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		s.internal(w, r, "build panel request", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.PanelToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		// Including "the panel killed us mid-reply", which is the successful case
		// wearing a failure's clothes. The interface polls /healthz rather than
		// believing either answer, so this only has to be honest about what it saw.
		s.log.Warn("panel restart call did not complete", "err", err)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"restarting": true,
			"note":       "the panel did not answer; it may already be restarting this container",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		s.log.Error("panel refused the restart", "status", resp.StatusCode)
		writeError(w, StatusUpstreamRefused, CodeInternal,
			fmt.Sprintf("the panel refused: HTTP %d — check VERDANDE_PANEL_TOKEN has control of this server", resp.StatusCode))
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"restarting": true})
}
