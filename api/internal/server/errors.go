package server

import (
	"encoding/json"
	"net/http"

	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Error codes used by the server itself, outside any endpoint.
const (
	CodeBadRequest       = "bad_request"
	CodeNotFound         = "not_found"
	CodeMethodNotAllowed = "method_not_allowed"
	CodeInternal         = "internal"
)

// writeError writes an Error-format JSON response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(openapi.Error{Error: openapi.ErrorDetail{Code: code, Message: message}})
}

func writeInternal(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, CodeInternal, "Something went wrong.")
}
