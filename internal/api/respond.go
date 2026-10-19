package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/lanory/lanory/internal/database"
)

// Standard error codes. These strings are part of the API contract: the
// frontend branches on them instead of parsing messages, so they must stay
// stable.
const (
	CodeBadRequest       = "bad_request"
	CodeUnauthorized     = "unauthorized"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeConflict         = "conflict"
	CodePayloadTooLarge  = "payload_too_large"
	CodeRateLimited      = "rate_limited"
	CodeInternal         = "internal_error"
	CodeNotImplemented   = "not_implemented"
	CodeUnavailable      = "unavailable"
)

// codeForStatus maps a status code onto its default machine code.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return CodeBadRequest
	case http.StatusUnauthorized:
		return CodeUnauthorized
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed
	case http.StatusConflict:
		return CodeConflict
	case http.StatusRequestEntityTooLarge:
		return CodePayloadTooLarge
	case http.StatusTooManyRequests:
		return CodeRateLimited
	case http.StatusServiceUnavailable:
		return CodeUnavailable
	case http.StatusNotImplemented:
		return CodeNotImplemented
	default:
		return CodeInternal
	}
}

// writeJSON writes v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		// A payload that cannot be marshalled is a programming error; fall
		// back to a plain internal error rather than emitting a truncated body.
		slog.Default().Error("marshal response failed", "error", err, "status", status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"response could not be encoded","code":"internal_error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\n"))
}

// writeError renders the standard error envelope.
//
// The message is always a human readable sentence, code is a stable machine
// code, and details is an optional map of field specific messages. Details is
// omitted entirely when empty rather than emitted as null.
func writeError(w http.ResponseWriter, status int, msg string, details map[string]string) {
	if msg == "" {
		msg = http.StatusText(status)
	}
	writeJSON(w, status, errorBody{Error: msg, Code: codeForStatus(status), Details: details})
}

// errorBody is the wire shape of the error envelope.
type errorBody struct {
	Error   string            `json:"error"`
	Code    string            `json:"code,omitempty"`
	Details map[string]string `json:"details,omitempty"`
}

// fail is a convenience wrapper for a plain error with no details.
func fail(w http.ResponseWriter, status int, msg string) {
	writeError(w, status, msg, nil)
}

// badRequest reports a client input mistake (400).
func badRequest(w http.ResponseWriter, msg string) {
	fail(w, http.StatusBadRequest, msg)
}

// badRequestField reports a client input mistake tied to one field (400).
func badRequestField(w http.ResponseWriter, field, msg string) {
	writeError(w, http.StatusBadRequest, msg, map[string]string{field: msg})
}

// notFound reports a missing resource (404).
func notFound(w http.ResponseWriter, what string) {
	fail(w, http.StatusNotFound, what+" not found")
}

// conflict reports a state conflict such as deleting a locked edge (409).
func conflict(w http.ResponseWriter, msg string) {
	fail(w, http.StatusConflict, msg)
}

// unauthorized reports a missing or wrong token (401).
func unauthorized(w http.ResponseWriter, msg string) {
	fail(w, http.StatusUnauthorized, msg)
}

// forbidden reports an authenticated request that is not permitted (403).
func forbidden(w http.ResponseWriter, msg string) {
	fail(w, http.StatusForbidden, msg)
}

// unavailable reports that a dependency is not wired or not reachable (503).
func unavailable(w http.ResponseWriter, msg string) {
	fail(w, http.StatusServiceUnavailable, msg)
}

// internalError reports an unexpected server failure (500) without leaking
// internals to the caller.
func internalError(w http.ResponseWriter, err error) {
	if err == nil {
		err = errors.New("unknown error")
	}
	slog.Default().Error("request failed", "error", err)
	fail(w, http.StatusInternalServerError, "internal server error")
}

// statusForError maps a store error onto an HTTP status.
func statusForError(err error) int {
	switch {
	case errors.Is(err, database.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

// storeError renders err from the store with the right status code.
func storeError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, database.ErrNotFound):
		notFound(w, what)
	default:
		internalError(w, err)
	}
}

// decodeJSON decodes a request body into dst.
//
// An empty body is not an error for PATCH semantics: dst keeps its zero value
// and the caller decides whether that is acceptable. Unknown fields are
// rejected so a typo in the frontend surfaces immediately instead of being
// silently ignored.
func decodeJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		if isBodyTooLarge(err) {
			return errBodyTooLarge
		}
		return err
	}
	return nil
}
