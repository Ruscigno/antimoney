package handlers

import (
	"encoding/json"
	"net/http"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, errorResponse{Error: message})
}

// WriteJSONPublic is the exported version for use by other packages.
func WriteJSONPublic(w http.ResponseWriter, code int, data interface{}) {
	writeJSON(w, code, data)
}

// WriteErrorPublic is the exported version for use by other packages.
func WriteErrorPublic(w http.ResponseWriter, code int, message string) {
	writeError(w, code, message)
}
