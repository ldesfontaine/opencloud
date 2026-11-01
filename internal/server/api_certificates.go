package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/tlsinspect"
)

type certificateInspectRequest struct {
	URL string `json:"url"`
}

// Ce que l'adresse a répondu au handshake. Joignable ou non, c'est un 200 :
// la réponse est le constat. cause n'a de sens que pour « unreachable », les
// trois autres champs que pour un certificat relevé.
type certificateInspectionJSON struct {
	Outcome     string     `json:"outcome"`
	Cause       string     `json:"cause"`
	Fingerprint string     `json:"fingerprint"`
	Subject     string     `json:"subject"`
	NotAfter    *time.Time `json:"not_after"`
}

// Relever le certificat d'une adresse https, sans clé : le serveur joint
// l'adresse (garde egress comprise), ce que le navigateur ne peut pas faire à
// sa place. Sert aux assistants Pare-feu et Proxmox, pour dire si l'adresse
// est joignable et proposer l'empreinte à confirmer. Réservé à l'opérateur :
// la route n'est pas de celles qu'un jeton d'API ouvre.
func (s *Server) inspectCertificate(w http.ResponseWriter, r *http.Request) {
	var request certificateInspectRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	report, err := tlsinspect.Inspect(r.Context(), request.URL)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, certificateInspectionJSON{
		Outcome: string(report.Outcome), Cause: report.Cause, Fingerprint: report.Fingerprint,
		Subject: report.Subject, NotAfter: httpx.TimeOrNil(report.NotAfter),
	})
}
