package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// APIError is the one shape every failure takes. A client that has learned to read
// it once can read all of them.
//
// `code` is a stable machine-readable string; `error` is English prose for a
// developer reading a log. Neither is what the user sees — the frontend maps `code`
// to a Danish or English message, because the server has no business deciding which
// language somebody's browser is in.
type APIError struct {
	Code    string            `json:"code"`
	Message string            `json:"error"`
	Fields  map[string]string `json:"fields,omitempty"`
}

func (e APIError) Error() string { return e.Message }

// The codes the API can return. Kept in one list so the frontend's mapping can be
// checked against it rather than discovered from whatever a handler happened to write.
const (
	CodeBadRequest      = "bad_request"
	CodeValidation      = "validation_failed"
	CodeUnauthorized    = "unauthorized"
	CodeTOTPRequired    = "totp_required"
	CodeForbidden       = "forbidden"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeRateLimited     = "rate_limited"
	CodeInternal        = "internal_error"
	CodePayloadTooLarge = "payload_too_large"

	// Four situations that used to answer plain `conflict`, which the frontend
	// renders as "that already exists". For these that is not merely vague, it is
	// wrong: an unconfigured Gmail is not a Gmail that is already there, and a
	// second factor that is off is not one that is on. A code exists so the
	// interface can say the true thing — the server's own prose is English for a
	// log, and deciding what a person reads is the frontend's job.
	CodeGmailNotConfigured = "gmail_not_configured"
	CodeAINotConfigured    = "ai_not_configured"
	CodeTOTPNotEnabled     = "totp_not_enabled"
	CodeInboxProtected     = "inbox_protected"
)

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, APIError{Code: code, Message: message})
}

func writeFieldErrors(w http.ResponseWriter, fields map[string]string) {
	writeJSON(w, http.StatusUnprocessableEntity, APIError{
		Code:    CodeValidation,
		Message: "one or more fields are not valid",
		Fields:  fields,
	})
}

// maxBodyBytes caps a JSON request body at 1 MiB. Task text and comments are small;
// anything larger is either a mistake or an attempt to make the server allocate.
// File uploads do not come through here.
const maxBodyBytes = 1 << 20

// decodeJSON reads a request body strictly: unknown fields are an error rather than
// being ignored.
//
// Silently dropping a field the client sent is the worst of the options — a typo in
// `priorty` becomes "the API accepted my request and ignored half of it", which is
// discovered much later and by the user rather than by the developer.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt := strings.TrimSpace(strings.Split(ct, ";")[0]); mt != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, CodeBadRequest,
				"request body must be application/json")
			return errors.New("wrong content type")
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			writeError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
				"request body is too large")
		case errors.Is(err, io.EOF):
			writeError(w, http.StatusBadRequest, CodeBadRequest, "request body is empty")
		default:
			writeError(w, http.StatusBadRequest, CodeBadRequest, jsonErrorMessage(err))
		}
		return err
	}

	// Exactly one JSON value, so a body with a second object appended cannot smuggle
	// anything past a handler that only reads the first.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, CodeBadRequest,
			"request body must contain a single JSON object")
		return errors.New("trailing data")
	}
	return nil
}

// jsonErrorMessage turns the standard library's decoder errors into something that
// names the offending field, since "json: cannot unmarshal string into Go struct
// field taskRequest.priority of type int" is not a sentence to put in an API.
func jsonErrorMessage(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		if typeErr.Field != "" {
			return "field " + typeErr.Field + " has the wrong type"
		}
		return "a field has the wrong type"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "request body is not valid JSON"
	}
	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		return "unknown field " + strings.TrimPrefix(err.Error(), "json: unknown field ")
	}
	return "request body could not be read"
}
