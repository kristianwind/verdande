package httpapi

import (
	"net/http"
	"time"

	"github.com/kristianwind/verdande/internal/auth"
)

// TOTP enrolment is three steps on purpose: begin, confirm, and only then is it on.
//
// Turning it on the moment a secret is generated would let somebody lock themselves
// out by closing the tab before their authenticator app had actually stored it. The
// secret is written to the user row but `totp_enabled` stays 0 until a code proves
// the app has it.

type totpSetupResponse struct {
	Secret string `json:"secret"`
	URI    string `json:"uri"`
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if user.TOTPEnabled {
		writeError(w, http.StatusConflict, CodeConflict,
			"two-factor authentication is already switched on")
		return
	}

	secret, uri, err := auth.NewTOTPSecret(s.issuer(), user.Email)
	if err != nil {
		s.internal(w, r, "generate totp secret", err)
		return
	}
	// Stored but not enabled: confirm decides that.
	if err := s.db.SetTOTPSecret(r.Context(), user.ID, secret, false); err != nil {
		s.internal(w, r, "store totp secret", err)
		return
	}
	writeJSON(w, http.StatusOK, totpSetupResponse{Secret: secret, URI: uri})
}

type totpConfirmResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

func (s *Server) handleTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	var req totpRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	if user.TOTPSecret == "" {
		writeError(w, http.StatusBadRequest, CodeBadRequest,
			"start setting up two-factor authentication first")
		return
	}
	if err := auth.VerifyTOTP(user.TOTPSecret, req.Code, time.Now()); err != nil {
		writeFieldErrors(w, map[string]string{"code": "not correct"})
		return
	}

	codes, hashes, err := auth.NewRecoveryCodes()
	if err != nil {
		s.internal(w, r, "generate recovery codes", err)
		return
	}
	if err := s.db.ReplaceRecoveryCodes(r.Context(), user.ID, hashes); err != nil {
		s.internal(w, r, "store recovery codes", err)
		return
	}
	if err := s.db.SetTOTPSecret(r.Context(), user.ID, user.TOTPSecret, true); err != nil {
		s.internal(w, r, "enable totp", err)
		return
	}

	// Shown once. The UI has to make that clear at this point, because there is no
	// way to show them again — only the hashes were kept.
	writeJSON(w, http.StatusOK, totpConfirmResponse{RecoveryCodes: codes})
}

type totpDisableRequest struct {
	Password string `json:"password"`
}

// handleTOTPDisable requires the password again. Switching off a second factor is
// exactly the action an attacker with a borrowed session would take first.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req totpDisableRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		writeFieldErrors(w, map[string]string{"password": "not correct"})
		return
	}
	if err := s.db.SetTOTPSecret(r.Context(), user.ID, "", false); err != nil {
		s.internal(w, r, "disable totp", err)
		return
	}
	if err := s.db.ReplaceRecoveryCodes(r.Context(), user.ID, nil); err != nil {
		s.log.Warn("clear recovery codes", "err", err, "user", user.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRecoveryCodesRegenerate issues a fresh set and retires the old one.
func (s *Server) handleRecoveryCodesRegenerate(w http.ResponseWriter, r *http.Request) {
	var req totpDisableRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	if !user.TOTPEnabled {
		writeError(w, http.StatusConflict, CodeTOTPNotEnabled,
			"two-factor authentication is not switched on")
		return
	}
	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		writeFieldErrors(w, map[string]string{"password": "not correct"})
		return
	}

	codes, hashes, err := auth.NewRecoveryCodes()
	if err != nil {
		s.internal(w, r, "generate recovery codes", err)
		return
	}
	if err := s.db.ReplaceRecoveryCodes(r.Context(), user.ID, hashes); err != nil {
		s.internal(w, r, "store recovery codes", err)
		return
	}
	writeJSON(w, http.StatusOK, totpConfirmResponse{RecoveryCodes: codes})
}

func (s *Server) handleRecoveryCodesCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.CountRecoveryCodes(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "count recovery codes", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"remaining": n})
}

// issuer is what an authenticator app lists the entry under. It is the instance's
// hostname rather than a bare "verdande", so somebody running two of them can tell
// the two entries apart in a list of a dozen.
func (s *Server) issuer() string {
	host := s.cfg.BaseURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(host) > len(prefix) && host[:len(prefix)] == prefix {
			host = host[len(prefix):]
			break
		}
	}
	if host == "" {
		return "verdande"
	}
	return "verdande (" + host + ")"
}
