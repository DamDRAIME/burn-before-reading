package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func parseUUIDParam(w http.ResponseWriter, r *http.Request, param string, errMsg string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, param))
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, errMsg)
		return uuid.Nil, false
	}
	return id, true
}
