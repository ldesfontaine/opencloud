package web

import (
	"encoding/json"
	"net/http"
)

// showHealth répond sans session : c'est ce que sonde « opencloud status ».
func (s *Server) showHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	health := map[string]string{"status": "ok", "version": s.version}
	if err := json.NewEncoder(w).Encode(health); err != nil {
		s.logger.Warn("write health response", "error", err)
	}
}
