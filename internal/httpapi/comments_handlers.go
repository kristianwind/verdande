package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/kristianwind/verdande/internal/store"
)

type commentJSON struct {
	ID          string           `json:"id"`
	TaskID      string           `json:"task_id"`
	UserID      string           `json:"user_id"`
	UserName    string           `json:"user_name"`
	UserColor   string           `json:"user_color"`
	Body        string           `json:"body"`
	Attachments []attachmentJSON `json:"attachments"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
}

type attachmentJSON struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
}

func toAttachmentJSON(a store.Attachment) attachmentJSON {
	return attachmentJSON{
		ID: a.ID, Filename: a.Filename, MimeType: a.MimeType, Size: a.Size,
		URL: "/api/v1/attachments/" + a.ID,
	}
}

func toCommentJSON(c store.Comment) commentJSON {
	j := commentJSON{
		ID: c.ID, TaskID: c.TaskID, UserID: c.UserID, UserName: c.UserName,
		UserColor: c.UserColor, Body: c.Body,
		Attachments: make([]attachmentJSON, 0, len(c.Attachments)),
		CreatedAt:   c.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   c.UpdatedAt.Format(time.RFC3339),
	}
	for _, a := range c.Attachments {
		j.Attachments = append(j.Attachments, toAttachmentJSON(a))
	}
	return j
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	if _, err := store.TaskRole(r.Context(), s.db, taskID, userFrom(r.Context()).ID); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	comments, err := s.db.ListComments(r.Context(), taskID)
	if err != nil {
		s.internal(w, r, "list comments", err)
		return
	}
	out := make([]commentJSON, 0, len(comments))
	for _, c := range comments {
		out = append(out, toCommentJSON(c))
	}

	files, err := s.db.ListTaskAttachments(r.Context(), taskID)
	if err != nil {
		s.internal(w, r, "list attachments", err)
		return
	}
	direct := make([]attachmentJSON, 0, len(files))
	for _, a := range files {
		direct = append(direct, toAttachmentJSON(a))
	}

	writeJSON(w, http.StatusOK, map[string]any{"comments": out, "attachments": direct})
}

type commentRequest struct {
	Body string `json:"body"`
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	body := strings.TrimSpace(req.Body)
	if body == "" {
		writeFieldErrors(w, map[string]string{"body": "required"})
		return
	}
	if utf8.RuneCountInString(body) > 10000 {
		writeFieldErrors(w, map[string]string{"body": "must be 10000 characters or fewer"})
		return
	}

	comment, err := s.db.CreateComment(r.Context(), taskID, user.ID, body)
	if err != nil {
		s.internal(w, r, "create comment", err)
		return
	}
	comment.UserName, comment.UserColor = user.Name, user.AvatarColor

	task, err := s.db.GetTask(r.Context(), taskID, user.ID)
	if err == nil {
		s.activity(r, task.ProjectID, taskID, "comment.created", map[string]any{"body": body})
		s.publish(task.ProjectID, "comment.created", toCommentJSON(*comment))
		// Everybody else who can see the project hears about it; the author does
		// not need telling what they just wrote.
		s.notifyProject(r, task.ProjectID, taskID, user.ID, "comment",
			user.Name+" kommenterede", task.Content)
	}
	writeJSON(w, http.StatusCreated, toCommentJSON(*comment))
}

func (s *Server) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	var req commentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		writeFieldErrors(w, map[string]string{"body": "required"})
		return
	}

	err := s.db.UpdateComment(r.Context(), chi.URLParam(r, "commentID"), userFrom(r.Context()).ID, body)
	if err != nil {
		s.storeError(w, r, "update comment", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	commentID := chi.URLParam(r, "commentID")
	user := userFrom(r.Context())

	taskID, err := s.db.CommentTask(r.Context(), commentID)
	if err != nil {
		s.storeError(w, r, "comment task", err)
		return
	}
	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	if err := s.db.DeleteComment(r.Context(), commentID, user.ID, role.CanManage()); err != nil {
		s.storeError(w, r, "delete comment", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- attachments -------------------------------------------------------------------

// maxUploadBytes caps a single file at 25 MiB.
//
// This is a to-do app on somebody's homelab, not a file host: the attachments that
// matter are a photo of a receipt or a PDF of a contract. A cap keeps one paste of
// a video from filling the volume the database lives on.
const maxUploadBytes = 25 << 20

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskID")
	user := userFrom(r.Context())

	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil || !role.CanEdit() {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that file is too large (25 MB maximum)")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeFieldErrors(w, map[string]string{"file": "required"})
		return
	}
	defer file.Close()

	if header.Size > maxUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"that file is too large (25 MB maximum)")
		return
	}

	stored, size, err := s.storeUpload(file)
	if err != nil {
		s.internal(w, r, "store upload", err)
		return
	}

	a := &store.Attachment{
		TaskID:     taskID,
		CommentID:  r.FormValue("comment_id"),
		Filename:   safeFilename(header.Filename),
		MimeType:   detectMime(header.Filename, header.Header.Get("Content-Type")),
		Size:       size,
		Path:       stored,
		UploadedBy: user.ID,
	}
	// An attachment hangs on a task or on a comment, never both — the schema says
	// so, and the form may have supplied a comment id.
	if a.CommentID != "" {
		a.TaskID = ""
	}

	if err := s.db.CreateAttachment(r.Context(), a); err != nil {
		os.Remove(filepath.Join(s.cfg.FilesDir(), stored))
		s.internal(w, r, "record attachment", err)
		return
	}

	if task, err := s.db.GetTask(r.Context(), taskID, user.ID); err == nil {
		s.activity(r, task.ProjectID, taskID, "attachment.added",
			map[string]any{"filename": a.Filename})
	}
	writeJSON(w, http.StatusCreated, toAttachmentJSON(*a))
}

// storeUpload writes the bytes under a content-addressed path.
//
// Naming by hash means the same file attached to ten tasks is stored once, and —
// more importantly — the path never contains anything the uploader chose. A
// filename is attacker-controlled text; using it to build a path is how "../"
// becomes a write outside the data directory.
func (s *Server) storeUpload(file io.Reader) (string, int64, error) {
	// Created here rather than assumed: startup makes the directory, but a volume
	// remounted underneath a running container would otherwise turn every upload
	// into a 500 with nothing in the log explaining why.
	if err := os.MkdirAll(s.cfg.FilesDir(), 0o750); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(s.cfg.FilesDir(), "upload-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once it has been renamed away

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), file)
	if err != nil {
		tmp.Close()
		return "", 0, err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	// Two levels of fan-out: tens of thousands of files in one directory is slow
	// to list and slow to open on most filesystems.
	rel := filepath.Join(sum[:2], sum[2:4], sum)
	full := filepath.Join(s.cfg.FilesDir(), rel)

	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", 0, err
	}
	if _, err := os.Stat(full); err == nil {
		// Already stored by an earlier upload of the identical file.
		return rel, size, nil
	}
	if err := os.Rename(tmpPath, full); err != nil {
		return "", 0, err
	}
	return rel, size, nil
}

// reachAttachment answers whether this person may see an attachment at all, and
// whether they may change it.
//
// An attachment hangs on exactly one of three things, and each has its own idea of
// who may reach it. A task's is governed by its project's roles, directly or
// through the comment it hangs on. A group's is governed by nothing but ownership:
// a group belongs to one person, is never shared, and a group that is not yours is
// not found — the same rule the rest of the group routes use.
//
// Both callers below used to resolve straight to a task, so a group's attachment
// answered 404 for its own owner.
func (s *Server) reachAttachment(r *http.Request, attachmentID string) (canEdit bool, err error) {
	user := userFrom(r.Context())

	a, err := s.db.GetAttachment(r.Context(), attachmentID)
	if err != nil {
		return false, err
	}

	if a.GroupID != "" {
		if _, err := s.db.GetProjectGroup(r.Context(), a.GroupID, user.ID); err != nil {
			return false, err
		}
		// Nobody else can reach it, so whoever can, can change it.
		return true, nil
	}

	// A file inside a note follows the note, which follows its project — the same
	// chain everything else here follows. Without this a picture imported with a
	// note answered 404 while sitting in the middle of the text that named it.
	if a.NoteID != "" {
		n, err := s.db.Note(r.Context(), a.NoteID)
		if err != nil {
			return false, err
		}
		if n == nil {
			return false, store.ErrNotFound
		}
		if n.ProjectID == "" {
			if n.CreatedBy != user.ID {
				return false, store.ErrNotFound
			}
			return true, nil
		}
		role, err := store.RequireProjectRole(r.Context(), s.db, n.ProjectID, user.ID, store.RoleViewer)
		if err != nil {
			return false, err
		}
		return role.CanEdit(), nil
	}

	taskID, err := s.db.AttachmentTask(r.Context(), attachmentID)
	if err != nil {
		return false, err
	}
	role, err := store.TaskRole(r.Context(), s.db, taskID, user.ID)
	if err != nil {
		return false, err
	}
	return role.CanEdit(), nil
}

func (s *Server) handleDownloadAttachment(w http.ResponseWriter, r *http.Request) {
	attachmentID := chi.URLParam(r, "attachmentID")

	if _, err := s.reachAttachment(r, attachmentID); err != nil {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	a, err := s.db.GetAttachment(r.Context(), attachmentID)
	if err != nil {
		s.storeError(w, r, "get attachment", err)
		return
	}

	// The stored path came from a hash this server computed, but it is still
	// checked against the files directory before being opened — a path traversal
	// that got into the database must not become one that reads /etc/passwd.
	full := filepath.Join(s.cfg.FilesDir(), filepath.Clean("/"+a.Path))
	if !strings.HasPrefix(full, s.cfg.FilesDir()+string(os.PathSeparator)) {
		s.log.Error("attachment path escapes the files directory", "id", a.ID, "path", a.Path)
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	f, err := os.Open(full)
	if err != nil {
		s.log.Error("open attachment", "err", err, "id", a.ID)
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}
	defer f.Close()

	// Almost everything is served as a download rather than rendered. An uploaded
	// SVG or HTML file displayed inline would run its own script on this origin,
	// with the session cookie attached — attachments are the one place a user
	// supplies content that another user opens.
	//
	// The exception is a short list of raster images, and it is not a softening of
	// that rule but what makes a note work: a note carries its pictures in the text
	// as `![](…)`, and served as octet-stream every one of them is a broken image.
	// The whole Apple Notes import exists to bring those pictures along.
	//
	// Safe because it is an allowlist of formats that cannot carry script, sent as
	// the exact type with `nosniff` — so the browser must treat it as that image or
	// not at all, and can never be talked into reading it as HTML. SVG is
	// deliberately absent: it is a document, not a picture, and it is the one image
	// type that can run script.
	disposition := "attachment"
	contentType := "application/octet-stream"
	if inlineImage[a.MimeType] {
		disposition = "inline"
		contentType = a.MimeType
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition+"; filename*=UTF-8''"+
		strings.ReplaceAll(mime.QEncoding.Encode("utf-8", a.Filename), " ", "%20"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprint(a.Size))
	w.Header().Set("Cache-Control", "private, max-age=3600")

	http.ServeContent(w, r, a.Filename, a.CreatedAt, f)
}

// inlineImage is what may be shown rather than downloaded.
//
// Raster formats only, and named one by one rather than matched on an "image/"
// prefix: the prefix would let image/svg+xml through, which is a document that
// can run script, and a list that grows by accident is how that happens.
var inlineImage = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
	"image/avif": true,
	// Sendes med, selv om ingen browser viser den endnu. Den er lige så ufarlig
	// som de andre, og en iPhone-note er fuld af dem — den dag Safari viser dem,
	// virker de, uden at nogen skal huske hvorfor de ikke gjorde.
	"image/heic": true,
	"image/heif": true,
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	attachmentID := chi.URLParam(r, "attachmentID")

	canEdit, err := s.reachAttachment(r, attachmentID)
	if err != nil || !canEdit {
		writeError(w, http.StatusNotFound, CodeNotFound, "not found")
		return
	}

	path, err := s.db.DeleteAttachment(r.Context(), attachmentID)
	if err != nil {
		s.storeError(w, r, "delete attachment", err)
		return
	}

	// The file is left on disk. It is content-addressed, so another attachment may
	// point at the same bytes; removing it here would break that one. A sweeper
	// for genuinely unreferenced files is a separate, safer job.
	s.log.Debug("attachment row removed; file retained", "path", path)
	w.WriteHeader(http.StatusNoContent)
}

// safeFilename keeps the name for display only — never for a path — but still
// strips the parts that would confuse a browser saving it.
func safeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '"' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "fil"
	}
	if utf8.RuneCountInString(name) > 200 {
		runes := []rune(name)
		name = string(runes[:200])
	}
	return name
}

// detectMime records what was declared, for display. It is never used to decide how
// to serve the file — see the download handler.
func detectMime(filename, declared string) string {
	if declared != "" && declared != "application/octet-stream" {
		return declared
	}
	if byExt := mime.TypeByExtension(filepath.Ext(filename)); byExt != "" {
		return byExt
	}
	return "application/octet-stream"
}
