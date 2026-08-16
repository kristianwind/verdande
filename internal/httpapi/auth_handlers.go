package httpapi

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kristianwind/verdande/internal/auth"
	"github.com/kristianwind/verdande/internal/store"
)

// userJSON is what the API says about a person. It deliberately does not carry the
// password hash or the TOTP secret: those are not "fields that happen to be
// private", they are values that must not leave the process, and the way to
// guarantee that is for the response type not to have them.
type userJSON struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AvatarColor string `json:"avatar_color"`
	Timezone    string `json:"timezone"`
	Locale      string `json:"locale"`
	IsAdmin     bool   `json:"is_admin"`
	TOTPEnabled bool   `json:"totp_enabled"`
	CreatedAt   string `json:"created_at"`
}

func toUserJSON(u *store.User) userJSON {
	return userJSON{
		ID: u.ID, Email: u.Email, Name: u.Name, AvatarColor: u.AvatarColor,
		Timezone: u.Timezone, Locale: u.Locale, IsAdmin: u.IsAdmin,
		TOTPEnabled: u.TOTPEnabled, CreatedAt: u.CreatedAt.Format(time.RFC3339),
	}
}

// --- sign in -----------------------------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	User         *userJSON `json:"user,omitempty"`
	TOTPRequired bool      `json:"totp_required"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	user, err := s.db.UserByEmail(r.Context(), req.Email)
	if err != nil || auth.VerifyPassword(user.PasswordHash, req.Password) != nil {
		// One message for "no such account" and for "wrong password". Telling them
		// apart turns the login form into a way to enumerate who has an account
		// here, which for a private instance is most of what an attacker wants.
		//
		// The timing still differs — a missing user skips the Argon2 work — and
		// closing that would mean hashing against a dummy on every miss. Not done:
		// it costs 19 MiB per probe and hands over an easy way to exhaust memory.
		writeError(w, http.StatusUnauthorized, CodeUnauthorized,
			"that email address and password do not match an account")
		return
	}

	// Argon2 parameters get raised over time; a login is the only moment the
	// plaintext exists and an upgrade is possible.
	if auth.NeedsRehash(user.PasswordHash) {
		if hash, err := auth.HashPassword(req.Password); err == nil {
			if err := s.db.UpdatePasswordHash(r.Context(), user.ID, hash, ""); err != nil {
				s.log.Warn("rehash password", "err", err, "user", user.ID)
			}
		}
	}

	s.loginLimiter.reset(clientIP(r))

	// A session that still needs a code gets a short life of its own: long enough
	// to fetch a phone, not long enough to be worth stealing.
	ttl := s.cfg.SessionTTL
	if user.TOTPEnabled {
		ttl = 10 * time.Minute
	}
	token, _, err := s.db.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r), ttl, user.TOTPEnabled)
	if err != nil {
		s.internal(w, "create session", err)
		return
	}
	s.setSessionCookie(w, token, ttl)

	if user.TOTPEnabled {
		writeJSON(w, http.StatusOK, loginResponse{TOTPRequired: true})
		return
	}
	uj := toUserJSON(user)
	writeJSON(w, http.StatusOK, loginResponse{User: &uj})
}

type totpRequest struct {
	Code string `json:"code"`
}

// handleLoginTOTP completes a two-step login. It runs behind requirePendingSession,
// so it is reachable only by a session that has already passed the password step.
func (s *Server) handleLoginTOTP(w http.ResponseWriter, r *http.Request) {
	var req totpRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())
	session := sessionFrom(r.Context())

	err := auth.VerifyTOTP(user.TOTPSecret, req.Code, time.Now())
	if err != nil {
		// A recovery code is accepted in the same field. Making somebody find a
		// different form while locked out of their account is a poor time to
		// introduce a second concept.
		used, rerr := s.db.UseRecoveryCode(r.Context(), user.ID, req.Code)
		if rerr != nil || !used {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized,
				"that verification code is not valid")
			return
		}
		s.log.Info("recovery code used", "user", user.ID)
	}

	if err := s.db.PromoteSession(r.Context(), session.ID, s.cfg.SessionTTL); err != nil {
		s.internal(w, "promote session", err)
		return
	}
	// The cookie is reissued so its lifetime matches the session's new one.
	if cookie, cerr := r.Cookie(s.cookieName()); cerr == nil {
		s.setSessionCookie(w, cookie.Value, s.cfg.SessionTTL)
	}

	uj := toUserJSON(user)
	writeJSON(w, http.StatusOK, loginResponse{User: &uj})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if session := sessionFrom(r.Context()); session != nil {
		if err := s.db.DeleteSession(r.Context(), session.ID); err != nil {
			s.log.Warn("delete session", "err", err)
		}
	}
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toUserJSON(userFrom(r.Context())))
}

// --- creating accounts --------------------------------------------------------

type signupRequest struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Timezone string `json:"timezone,omitempty"`
	Locale   string `json:"locale,omitempty"`
}

// handleSignup creates an account from an invite. There is no open registration:
// the email address comes from the invite, not from the form, so an invite cannot
// be redirected to somebody else.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req signupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	invite, err := s.db.InviteByToken(r.Context(), req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest,
			"this invite link is not valid or has expired")
		return
	}

	fields := map[string]string{}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		fields["name"] = "required"
	}
	if msg := checkPassword(req.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, "hash password", err)
		return
	}

	user := &store.User{
		Email: invite.Email, Name: name, PasswordHash: hash,
		Timezone: req.Timezone, Locale: req.Locale,
	}
	if err := s.db.CreateUser(r.Context(), user, inboxName(req.Locale)); err != nil {
		if errors.Is(err, store.ErrEmailInUse) {
			writeError(w, http.StatusConflict, CodeConflict,
				"an account already exists for that email address")
			return
		}
		s.internal(w, "create user", err)
		return
	}

	// Accepting the invite is what adds the membership and burns the token, in one
	// transaction: a signup that half-succeeded would leave a usable invite behind.
	if err := s.db.AcceptInvite(r.Context(), invite.ID, user.ID); err != nil {
		s.internal(w, "accept invite", err)
		return
	}

	s.startSession(w, r, user)
}

type bootstrapRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Timezone string `json:"timezone,omitempty"`
	Locale   string `json:"locale,omitempty"`
}

// handleBootstrap creates the first administrator, and only ever the first: the
// endpoint refuses once any account exists. That is what makes it safe to leave
// unauthenticated — it has to be, because there is nobody to authenticate as yet.
//
// The race matters. Two requests arriving together must not both see an empty
// database, so the check and the insert happen in one transaction inside the store.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req bootstrapRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	fields := map[string]string{}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		fields["email"] = "must be an email address"
	}
	if strings.TrimSpace(req.Name) == "" {
		fields["name"] = "required"
	}
	if msg := checkPassword(req.Password); msg != "" {
		fields["password"] = msg
	}
	if len(fields) > 0 {
		writeFieldErrors(w, fields)
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, "hash password", err)
		return
	}

	user := &store.User{
		Email: req.Email, Name: strings.TrimSpace(req.Name), PasswordHash: hash,
		IsAdmin: true, Timezone: req.Timezone, Locale: req.Locale,
	}
	if err := s.db.CreateFirstAdmin(r.Context(), user, inboxName(req.Locale)); err != nil {
		if errors.Is(err, store.ErrAlreadySetUp) {
			writeError(w, http.StatusConflict, CodeConflict, "this instance is already set up")
			return
		}
		s.internal(w, "create first admin", err)
		return
	}

	s.log.Info("first administrator created", "user", user.ID, "email", user.Email)
	s.startSession(w, r, user)
}

// handleSetupState tells the sign-in page whether to offer setup or a login form.
// It reveals only whether any account exists, which anyone can determine anyway by
// looking at whether the setup form works.
func (s *Server) handleSetupState(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.UserCount(r.Context())
	if err != nil {
		s.internal(w, "count users", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": n == 0})
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User) {
	token, _, err := s.db.CreateSession(r.Context(), user.ID, r.UserAgent(), clientIP(r), s.cfg.SessionTTL, false)
	if err != nil {
		s.internal(w, "create session", err)
		return
	}
	s.setSessionCookie(w, token, s.cfg.SessionTTL)
	uj := toUserJSON(user)
	writeJSON(w, http.StatusCreated, loginResponse{User: &uj})
}

// --- password reset -----------------------------------------------------------

type forgotRequest struct {
	Email string `json:"email"`
}

// handleForgotPassword always answers the same way.
//
// Saying "no account with that address" would turn this into an account-existence
// oracle that needs no password at all. The cost is that somebody who mistypes
// their address waits for an email that never comes, which is the better failure.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	user, err := s.db.UserByEmail(r.Context(), req.Email)
	if err == nil {
		token, err := s.db.CreatePasswordReset(r.Context(), user.ID, s.cfg.ResetTTL)
		if err != nil {
			s.internal(w, "create password reset", err)
			return
		}
		link := s.cfg.BaseURL + "/reset?token=" + token
		if err := s.mail.SendPasswordReset(r.Context(), user.Email, user.Name, link, s.cfg.ResetTTL); err != nil {
			s.log.Error("send password reset", "err", err, "user", user.ID)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "if that address has an account, a reset link is on its way",
	})
}

type resetRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	if msg := checkPassword(req.Password); msg != "" {
		writeFieldErrors(w, map[string]string{"password": msg})
		return
	}

	userID, err := s.db.UsePasswordReset(r.Context(), req.Token)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest,
			"this reset link is not valid or has expired")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		s.internal(w, "hash password", err)
		return
	}
	// Every session ends: a password reset is what somebody does when they think
	// another person has been in their account.
	if err := s.db.UpdatePasswordHash(r.Context(), userID, hash, ""); err != nil {
		s.internal(w, "update password", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	// The current password is required even though the caller is signed in: it is
	// what stops an unattended laptop from becoming a permanent account takeover.
	if err := auth.VerifyPassword(user.PasswordHash, req.CurrentPassword); err != nil {
		writeFieldErrors(w, map[string]string{"current_password": "not correct"})
		return
	}
	if msg := checkPassword(req.NewPassword); msg != "" {
		writeFieldErrors(w, map[string]string{"new_password": msg})
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.internal(w, "hash password", err)
		return
	}
	keep := ""
	if session := sessionFrom(r.Context()); session != nil {
		keep = session.ID
	}
	if err := s.db.UpdatePasswordHash(r.Context(), user.ID, hash, keep); err != nil {
		s.internal(w, "update password", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- validation ---------------------------------------------------------------

// Password rules, and only these.
//
// A length floor and nothing else is deliberate. Composition rules — a digit, a
// symbol, a capital — measurably push people towards "Password1!" and are no longer
// recommended by NIST or OWASP. Length is what actually helps, and a passphrase
// must not be rejected for lacking punctuation.
const (
	minPasswordLength = 10
	maxPasswordLength = 1024 // Argon2 will hash anything; this bounds the work
)

func checkPassword(p string) string {
	switch {
	case p == "":
		return "required"
	case utf8.RuneCountInString(p) < minPasswordLength:
		return "must be at least 10 characters"
	case len(p) > maxPasswordLength:
		return "is too long"
	}
	return ""
}

// inboxName is the one string that has to be right at account creation, before the
// user has ever seen a settings page to state a preference on.
func inboxName(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "en") {
		return "Inbox"
	}
	return "Indbakke"
}

// internal logs the real error and tells the client nothing about it. The detail
// belongs in the log, where the operator can see it; in the response it would be a
// description of the server's internals handed to whoever asked.
func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, CodeInternal, "something went wrong")
}
