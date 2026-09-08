package httpapi

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

// markShared fills in the two fields the list needs to show a note somebody else
// shared with the reader: that it is such a note, and whose it is.
//
// Done in one pass over the page rather than per note: one query asks which of
// these ids are shared with the reader, and one asks for the owners' names and
// colours. A note the reader owns is never "shared with me", however it was
// reached — the group is other people's notes, not one's own.
func (s *Server) markShared(r *http.Request, notes []store.Note) []store.Note {
	me := userFrom(r.Context()).ID
	if len(notes) == 0 {
		return notes
	}

	ids := make([]string, 0, len(notes))
	for _, n := range notes {
		if n.CreatedBy != me {
			ids = append(ids, n.ID)
		}
	}
	shared, err := s.db.NotesSharedWith(r.Context(), me, ids)
	if err != nil {
		// The list is worth more than the chip: a note without its "shared" mark
		// still reads and still opens. Log nothing here — the store already did.
		return notes
	}

	owners := map[string]store.Person{}
	if len(shared) > 0 {
		ownerIDs := make([]string, 0, len(shared))
		for _, n := range notes {
			if shared[n.ID] && n.CreatedBy != "" {
				ownerIDs = append(ownerIDs, n.CreatedBy)
			}
		}
		owners, _ = s.db.PeopleByIDs(r.Context(), ownerIDs)
	}

	for i := range notes {
		if !shared[notes[i].ID] {
			continue
		}
		notes[i].SharedWithMe = true
		if p, ok := owners[notes[i].CreatedBy]; ok {
			owner := p
			notes[i].Owner = &owner
		}
	}
	return notes
}

// handleListNoteShares is who a note is shared with. Owner only: the list of people
// who can see your note is itself something only you should see.
func (s *Server) handleListNoteShares(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	// 404, not 403: telling a stranger "not yours" still confirms the note exists.
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	shares, err := s.db.ListNoteShares(r.Context(), n.ID)
	if err != nil {
		s.internal(w, r, "list note shares", err)
		return
	}
	// Regnet forfra, én gang, og læst to veje ud af det samme svar: hvem der har
	// *denne* note, fordi en anden peger på den, og hvilke noter der omvendt følger
	// med den her. To udregninger af det samme er to, der kan blive uenige.
	//
	// Og forfra frem for læst af basen, så panelet viser det, der gælder nu — også
	// når linkene er skrevet af en anden og delingerne derfor ikke er regnet om
	// siden.
	following, err := s.db.SyncLinkedShares(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "sync linked shares", err)
		return
	}
	via := map[string]string{}
	for _, l := range following {
		if l.NoteID == n.ID {
			via[l.UserID] = l.Via
		}
	}

	out := make([]noteShareJSON, 0, len(shares))
	already := map[string]bool{}
	for _, sh := range shares {
		// Panelet skal kunne sige forskel: en deling, nogen har valgt, tages tilbage
		// ved at fjerne den — en, der er fulgt med, ved at fjerne linket eller den
		// deling, den kom fra. En knap, der lover det første og gør det andet, er
		// værre end ingen knap.
		out = append(out, noteShareJSON{
			User:   personJSON{ID: sh.User.ID, Name: sh.User.Name, AvatarColor: sh.User.AvatarColor},
			Role:   string(sh.Role),
			Linked: via[sh.User.ID] != "",
			Via:    via[sh.User.ID],
		})
		already[sh.User.ID] = true
	}

	// The picker is filled from the same response, so the panel opens complete in
	// one round-trip. People already on the note are left out — they are in the list
	// above it, not the "add someone" menu.
	candidates, err := s.db.UsersForSharing(r.Context(), userFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, "share candidates", err)
		return
	}
	cand := make([]personJSON, 0, len(candidates))
	for _, p := range candidates {
		if already[p.ID] {
			continue
		}
		cand = append(cand, personJSON{ID: p.ID, Name: p.Name, AvatarColor: p.AvatarColor})
	}

	// De inviterede står ved siden af dem, der er kommet. Uden dem er en
	// invitation sendt i går usynlig, og så sender man den igen — endnu et link til
	// den samme indbakke, og ingen af dem til at trække tilbage.
	invites, err := s.db.ListNoteInvites(r.Context(), n.ID)
	if err != nil {
		s.internal(w, r, "list note invites", err)
		return
	}
	pending := make([]noteInviteJSON, 0, len(invites))
	for _, i := range invites {
		pending = append(pending, noteInviteJSON{ID: i.ID, Email: i.Email, Role: string(i.Role)})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"shares":     out,
		"candidates": cand,
		"invites":    pending,
		"follows":    followsOf(following, n.ID),
	})
}

// followsOf er de noter, der følger med *denne* note — én linje pr. note, ikke én
// pr. note og person. Panelet spørger på vegne af noten, og svaret "Prisliste
// følger med" er det samme, hvem det så end deles med.
func followsOf(all []store.LinkedShare, noteID string) []followJSON {
	seen := map[string]bool{}
	out := []followJSON{}
	for _, l := range all {
		if l.ViaID != noteID || seen[l.NoteID] {
			continue
		}
		seen[l.NoteID] = true
		out = append(out, followJSON{NoteID: l.NoteID, Title: l.Title})
	}
	return out
}

type followJSON struct {
	NoteID string `json:"note_id"`
	Title  string `json:"title"`
}

type noteShareJSON struct {
	User personJSON `json:"user"`
	Role string     `json:"role"`
	// Sat, når personen har noten, fordi en anden delt note peger på den — ikke
	// fordi nogen har delt netop den. `via` er den note, der peger.
	Linked bool   `json:"linked"`
	Via    string `json:"via,omitempty"`
}

type shareNoteRequest struct {
	UserID string `json:"user_id"`
	// Email deles der med, når personen ikke er at finde i listen — enten fordi
	// man kender adressen bedre end navnet, eller fordi der ikke er nogen konto at
	// finde endnu.
	Email string `json:"email"`
	Role  string `json:"role"`
}

// noteInviteJSON er en deling, der venter på en konto. Der er ikke noget navn og
// ingen farve at vise: adressen er alt, hvad der findes om personen indtil de
// dukker op.
type noteInviteJSON struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// handleShareNote grants a person a role on a note, or changes the role they hold.
//
// Der er to måder at sige, hvem det er. `user_id` er den, man har peget på i
// listen. `email` er den, man kender adressen på — og det er den, der også virker,
// når personen ikke har nogen konto: så bliver delingen til en invitation, der
// venter, og bliver til en rigtig deling i det øjeblik, kontoen bliver oprettet.
//
// Det er ikke en ny magt til nogen. Enhver, der ejer et projekt, kunne i forvejen
// invitere en fremmed ind på instansen pr. e-mail; det her er den samme handling
// på en note, der ellers krævede tre skridt og en ventetid: invitér til instansen,
// vent på at de opretter sig, find noten frem, del den.
func (s *Server) handleShareNote(w http.ResponseWriter, r *http.Request) {
	var req shareNoteRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}

	role := store.Role(req.Role)
	if role == "" {
		role = store.RoleViewer
	}
	if role != store.RoleViewer && role != store.RoleEditor {
		writeFieldErrors(w, map[string]string{"role": "must be viewer or editor"})
		return
	}

	me := userFrom(r.Context())
	email := store.NormalizeEmail(req.Email)

	if req.UserID == "" && email == "" {
		writeFieldErrors(w, map[string]string{"user_id": "required"})
		return
	}

	// En adresse, der viser sig at høre til en konto, er den konto. At sende dem
	// gennem en oprettelsesside, de ikke kan komme igennem, ville være en blindgyde
	// — samme regel som en projektinvitation følger.
	if req.UserID == "" {
		if !strings.Contains(email, "@") {
			writeFieldErrors(w, map[string]string{"email": "must be an email address"})
			return
		}
		if email == store.NormalizeEmail(me.Email) {
			writeFieldErrors(w, map[string]string{"email": "you already have this note"})
			return
		}
		existing, err := s.db.UserByEmail(r.Context(), email)
		if err != nil {
			s.inviteToNote(w, r, n, email, role)
			return
		}
		req.UserID = existing.ID
	}

	// The recipient must be a real account and not the sharer themselves; ShareNote
	// refuses the owner, and a made-up id should not reach it.
	if req.UserID == me.ID {
		writeFieldErrors(w, map[string]string{"user_id": "you already have this note"})
		return
	}
	person, err := s.db.PersonByID(r.Context(), req.UserID)
	if err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such person")
		return
	}

	if err := s.db.ShareNote(r.Context(), n.ID, req.UserID, role, me.ID); err != nil {
		s.storeError(w, r, "share note", err)
		return
	}
	s.notifyNoteShared(r, n, req.UserID)

	// Og det, noten peger på, følger med. Svaret siger hvad — en deling, der stille
	// tog tre noter mere med, ville være rigtig og alligevel ikke til at overskue.
	followed, err := s.db.SyncLinkedShares(r.Context(), me.ID)
	if err != nil {
		s.internal(w, r, "sync linked shares", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"follows": followsOf(followed, n.ID),
		// Personen sendes med retur, fordi fladen kan have delt med en adresse og
		// derfor ikke selv ved, hvem den ramte.
		"user": personJSON{ID: person.ID, Name: person.Name, AvatarColor: person.AvatarColor},
		"role": string(role),
	})
}

// inviteToNote sends a link that creates an account and the share at once.
//
// Linket vises i svaret, når der ikke er nogen post at sende det med. En
// invitation, der forsvandt, fordi instansen ikke har en SMTP-server, ville se ud
// som om den var sendt — og først blive opdaget som ikke-sendt af den, der ventede
// på den.
func (s *Server) inviteToNote(w http.ResponseWriter, r *http.Request, n *store.Note, email string, role store.Role) {
	me := userFrom(r.Context())

	// En anden invitation til den samme note i den samme indbakke er ikke en
	// invitation mere: det er to links, der gør det samme, og kun det ene kan
	// trækkes tilbage ad gangen.
	pending, err := s.db.PendingNoteInvite(r.Context(), n.ID, email)
	if err != nil {
		s.internal(w, r, "pending note invite", err)
		return
	}
	if pending {
		writeError(w, http.StatusConflict, CodeConflict, "that address has already been invited to this note")
		return
	}

	token, inv, err := s.db.CreateNoteInvite(r.Context(), email, n.ID, role, me.ID, s.cfg.InviteTTL)
	if err != nil {
		s.internal(w, r, "create note invite", err)
		return
	}
	link := s.cfg.BaseURL + "/invite?token=" + token

	emailed := false
	if s.mail.Configured() {
		if err := s.mail.SendNoteInvite(r.Context(), email, me.Name, n.Title, link, s.cfg.InviteTTL); err != nil {
			s.log.Error("send note invite", "err", err, "to", email)
		} else {
			emailed = true
		}
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"invited": noteInviteJSON{ID: inv.ID, Email: inv.Email, Role: string(inv.Role)},
		"link":    link,
		"emailed": emailed,
	})
}

// handleDeleteNoteInvite withdraws an invitation before it is used.
//
// Det er den eneste vej tilbage. Kun aftrykket af tokenet er gemt, så et link, der
// er sendt til den forkerte adresse, kan ikke findes frem og gøres ugyldigt — det
// kan kun rækken, det hører til.
func (s *Server) handleDeleteNoteInvite(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	if err := s.db.DeleteNoteInvite(r.Context(), n.ID, chi.URLParam(r, "inviteID")); err != nil {
		s.storeError(w, r, "delete note invite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUnshareNote takes a person's access away again.
func (s *Server) handleUnshareNote(w http.ResponseWriter, r *http.Request) {
	n, err := s.db.Note(r.Context(), chi.URLParam(r, "noteID"))
	if err != nil {
		s.internal(w, r, "get note", err)
		return
	}
	if n == nil || !s.ownsNote(r, n) {
		writeError(w, http.StatusNotFound, CodeNotFound, "no such note")
		return
	}
	if err := s.db.UnshareNote(r.Context(), n.ID, chi.URLParam(r, "userID")); err != nil {
		s.internal(w, r, "unshare note", err)
		return
	}
	// Det, der fulgte med, følger også med tilbage. Regnet forfra: de udledte rækker
	// skrives om fra bunden, så en note, der stadig kan nås fra en anden deling,
	// bliver stående, og en, der ikke kan, forsvinder.
	if _, err := s.db.SyncLinkedShares(r.Context(), userFrom(r.Context()).ID); err != nil {
		s.internal(w, r, "sync linked shares", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
