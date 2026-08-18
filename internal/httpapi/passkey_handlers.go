package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/kristianwind/verdande/internal/auth"
	"github.com/kristianwind/verdande/internal/store"
)

// Passkeys: a key on a device instead of a secret in a head.
//
// Four endpoints, in two pairs. Registration is done by somebody already signed in
// — you cannot be handed a way into an account you have not proved you own.
// Logging in is done by somebody who is not, and is the one place here that is
// reachable without a session.
//
// Each ceremony is two requests because the browser has to talk to the
// authenticator in between: the server issues a challenge, the device signs it,
// the server checks the signature against what it remembers issuing. The challenge
// lives in the database rather than in a cookie, because the whole point of it is
// that the server chose it and remembers choosing it.

const challengeTTL = 5 * time.Minute

// passkeyAvailable answers 503 rather than 404 when the relying party could not be
// built. 404 would read as "this version does not have passkeys"; the truth is
// that this deployment cannot offer them, which is a different thing to fix.
func (s *Server) passkeyAvailable(w http.ResponseWriter, r *http.Request) bool {
	if s.passkeys != nil {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, CodeInternal,
		"passkeys are unavailable on this instance: VERDANDE_BASE_URL is not an address an authenticator will accept")
	return false
}

type passkeyJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// AAGUID identifies the authenticator model, which is what lets the list say
	// something recognisable beside a key somebody named three months ago.
	AAGUID       string `json:"aaguid,omitempty"`
	Discoverable bool   `json:"discoverable"`
	UserVerified bool   `json:"user_verified"`
	CreatedAt    string `json:"created_at"`
	LastUsedAt   string `json:"last_used_at,omitempty"`
}

func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.db.ListPasskeys(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "list passkeys", err)
		return
	}
	out := make([]passkeyJSON, 0, len(keys))
	for _, k := range keys {
		j := passkeyJSON{
			ID: k.ID, Name: k.Name, AAGUID: k.AAGUID,
			Discoverable: k.Discoverable, UserVerified: k.UserVerified,
			CreatedAt: k.CreatedAt.Format(time.RFC3339),
		}
		if !k.LastUsedAt.IsZero() {
			j.LastUsedAt = k.LastUsedAt.Format(time.RFC3339)
		}
		out = append(out, j)
	}
	// Whether this deployment can offer them at all, alongside the ones that exist.
	// The interface asks one question rather than discovering the answer from a 503
	// it triggered by offering a button — an address an authenticator refuses is a
	// deployment fact, not an error somebody caused.
	writeJSON(w, http.StatusOK, map[string]any{
		"passkeys": out, "available": s.passkeys != nil,
	})
}

// --- registering a key ------------------------------------------------------------

func (s *Server) handleBeginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeyAvailable(w, r) {
		return
	}
	user := userFrom(r.Context())

	account, err := s.passkeyUser(r, user.ID, user.Email, user.Name)
	if err != nil {
		s.internal(w, r, "load passkeys", err)
		return
	}

	// Resident keys, and the authenticator asked to verify its owner. Both are
	// what make this a *passkey* rather than a second factor: only a discoverable
	// credential can start a login with no email typed, and only a verified one
	// stands in for the password rather than beside it.
	creation, session, err := s.passkeys.BeginRegistration(account,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		}),
		// Excluding what they already have, so a second registration on the same
		// device replaces nothing and reports itself instead of silently making a
		// duplicate.
		webauthn.WithExclusions(account.excludeList()),
	)
	if err != nil {
		s.internal(w, r, "begin passkey registration", err)
		return
	}
	s.issueChallenge(w, r, user.ID, "register", session, creation)
}

type finishRegistrationRequest struct {
	ChallengeID string          `json:"challenge_id"`
	Name        string          `json:"name"`
	Credential  json.RawMessage `json:"credential"`
}

func (s *Server) handleFinishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.passkeyAvailable(w, r) {
		return
	}
	var req finishRegistrationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	user := userFrom(r.Context())

	session, ok := s.takeChallenge(w, r, req.ChallengeID, "register", user.ID)
	if !ok {
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(strings.NewReader(string(req.Credential)))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "that response could not be read")
		return
	}

	account, err := s.passkeyUser(r, user.ID, user.Email, user.Name)
	if err != nil {
		s.internal(w, r, "load passkeys", err)
		return
	}

	credential, err := s.passkeys.CreateCredential(account, *session, parsed)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "that key could not be verified")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Passkey"
	}
	key := &store.Passkey{
		UserID:       user.ID,
		CredentialID: auth.EncodeCredentialID(credential.ID),
		PublicKey:    credential.PublicKey,
		AAGUID:       auth.EncodeCredentialID(credential.Authenticator.AAGUID),
		SignCount:    credential.Authenticator.SignCount,
		Discoverable: credential.Flags.BackupEligible || parsed.Response.AttestationObject.AuthData.Flags.HasBackupState(),
		UserVerified: credential.Flags.UserVerified,
		Name:         name,
	}
	if err := s.db.CreatePasskey(r.Context(), key); err != nil {
		s.internal(w, r, "store passkey", err)
		return
	}
	writeJSON(w, http.StatusCreated, passkeyJSON{
		ID: key.ID, Name: key.Name, AAGUID: key.AAGUID,
		Discoverable: key.Discoverable, UserVerified: key.UserVerified,
		CreatedAt: key.CreatedAt.Format(time.RFC3339),
	})
}

// --- signing in with a key ---------------------------------------------------------

func (s *Server) handleBeginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeyAvailable(w, r) {
		return
	}

	// Discoverable: nobody has said who they are yet, and that is the point. The
	// device knows which account its key belongs to, so there is no email to type
	// and no list of accounts for anybody to probe.
	assertion, session, err := s.passkeys.BeginDiscoverableLogin()
	if err != nil {
		s.internal(w, r, "begin passkey login", err)
		return
	}
	s.issueChallenge(w, r, "", "login", session, assertion)
}

type finishLoginRequest struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
}

func (s *Server) handleFinishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeyAvailable(w, r) {
		return
	}
	var req finishLoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}

	session, ok := s.takeChallenge(w, r, req.ChallengeID, "login", "")
	if !ok {
		return
	}

	parsed, err := protocol.ParseCredentialRequestResponseBody(strings.NewReader(string(req.Credential)))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "that response could not be read")
		return
	}

	// Which account, resolved from the credential the device signed with. The
	// handler is given the raw id and returns the owner; the library checks the
	// signature against that owner's stored public key.
	var matched *store.Passkey
	credential, err := s.passkeys.ValidateDiscoverableLogin(
		func(rawID, userHandle []byte) (webauthn.User, error) {
			key, err := s.db.PasskeyByCredentialID(r.Context(), auth.EncodeCredentialID(rawID))
			if err != nil {
				return nil, err
			}
			matched = key
			owner, err := s.db.UserByID(r.Context(), key.UserID)
			if err != nil {
				return nil, err
			}
			return s.passkeyUser(r, owner.ID, owner.Email, owner.Name)
		}, *session, parsed)
	if err != nil || matched == nil {
		// One message, as with a wrong password: telling "no such key" apart from
		// "bad signature" is a way to learn which credentials exist here.
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "that key was not accepted")
		return
	}

	// A counter that goes backwards means the credential has been cloned. Not
	// every authenticator keeps one, so zero on both sides is silence rather than
	// suspicion — but a real counter that has gone down is the one signal this
	// design gives, and it is refused rather than logged.
	if credential.Authenticator.CloneWarning {
		s.log.Warn("passkey clone warning", "passkey", matched.ID, "user", matched.UserID)
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "that key was not accepted")
		return
	}

	owner, err := s.db.UserByID(r.Context(), matched.UserID)
	if err != nil {
		s.internal(w, r, "load account", err)
		return
	}

	if err := s.db.TouchPasskey(r.Context(), matched.ID, credential.Authenticator.SignCount); err != nil {
		s.log.Warn("record passkey use", "err", err)
	}
	s.loginLimiter.reset(clientIP(r))

	// A verified key is both factors at once: the device proved possession and the
	// person proved they were there, which is exactly what a password plus a code
	// is for. One that only proved possession still stops at the code.
	pending := owner.TOTPEnabled && !credential.Flags.UserVerified
	ttl := s.cfg.SessionTTL
	if pending {
		ttl = 10 * time.Minute
	}
	token, _, err := s.db.CreateSession(r.Context(), owner.ID, r.UserAgent(), clientIP(r), ttl, pending)
	if err != nil {
		s.internal(w, r, "create session", err)
		return
	}
	s.setSessionCookie(w, token, ttl)

	if pending {
		writeJSON(w, http.StatusOK, loginResponse{TOTPRequired: true})
		return
	}
	uj := toUserJSON(owner)
	writeJSON(w, http.StatusOK, loginResponse{User: &uj})
}

// --- managing them ------------------------------------------------------------------

func (s *Server) handleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeFieldErrors(w, map[string]string{"name": "required"})
		return
	}
	if err := s.db.RenamePasskey(r.Context(), chi.URLParam(r, "passkeyID"),
		userFrom(r.Context()).ID, name); err != nil {
		s.storeError(w, r, "rename passkey", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	if err := s.db.DeletePasskey(r.Context(), chi.URLParam(r, "passkeyID"),
		userFrom(r.Context()).ID); err != nil {
		s.storeError(w, r, "delete passkey", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- shared plumbing -----------------------------------------------------------------

// passkeyAccount is the library's view of an account, plus the rows behind it.
type passkeyAccount struct {
	*auth.PasskeyUser
	stored []store.Passkey
}

func (a *passkeyAccount) excludeList() []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(a.stored))
	for _, c := range a.PasskeyUser.Credentials {
		out = append(out, c.Descriptor())
	}
	return out
}

func (s *Server) passkeyUser(r *http.Request, userID, email, name string) (*passkeyAccount, error) {
	keys, err := s.db.ListPasskeys(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	credentials := make([]webauthn.Credential, 0, len(keys))
	for _, k := range keys {
		raw, err := auth.DecodeCredentialID(k.CredentialID)
		if err != nil {
			continue
		}
		credentials = append(credentials, webauthn.Credential{
			ID:        raw,
			PublicKey: k.PublicKey,
			Authenticator: webauthn.Authenticator{
				SignCount: k.SignCount,
			},
		})
	}
	return &passkeyAccount{
		PasskeyUser: &auth.PasskeyUser{
			ID: userID, Email: email, Name: name, Credentials: credentials,
		},
		stored: keys,
	}, nil
}

// issueChallenge stores the server's side of a ceremony and hands the browser the
// options plus the id it will answer with.
func (s *Server) issueChallenge(w http.ResponseWriter, r *http.Request, userID, purpose string, session *webauthn.SessionData, options any) {
	encoded, err := json.Marshal(session)
	if err != nil {
		s.internal(w, r, "encode webauthn session", err)
		return
	}
	id, err := s.db.StoreChallenge(r.Context(), userID, string(session.Challenge), purpose, encoded, challengeTTL)
	if err != nil {
		s.internal(w, r, "store challenge", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"challenge_id": id, "options": options})
}

// takeChallenge reads a challenge once and checks it belongs where it is being
// used. A registration challenge answered by a login — or one account's answered
// by another — is refused rather than merely failing later.
func (s *Server) takeChallenge(w http.ResponseWriter, r *http.Request, id, purpose, userID string) (*webauthn.SessionData, bool) {
	owner, raw, err := s.db.TakeChallenge(r.Context(), id, purpose)
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadRequest,
			"that attempt has expired or was already used; start again")
		return nil, false
	}
	if userID != "" && owner != userID {
		writeError(w, http.StatusBadRequest, CodeBadRequest, "that attempt belongs to another account")
		return nil, false
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(raw, &session); err != nil {
		s.internal(w, r, "decode webauthn session", err)
		return nil, false
	}
	return &session, true
}
