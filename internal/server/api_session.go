package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Ce que le front lit au démarrage : la version, la langue réglée et les
// seuils du produit.
type sessionResponse struct {
	Version   string   `json:"version"`
	Language  string   `json:"language"`
	Languages []string `json:"languages"`
	// Les jours à partir desquels un certificat se signale. Servis ici
	// pour que le compteur de la vue d'ensemble et la couleur d'une ligne
	// basculent sur le même chiffre, tenu à un seul endroit.
	Certificates certificateThresholds `json:"certificate_thresholds"`
}

type certificateThresholds struct {
	Warning int `json:"warning"`
	Danger  int `json:"danger"`
}

type languageRequest struct {
	Language string `json:"language"`
}

// Les compteurs de la barre latérale et de la vue d'ensemble.
type countsResponse struct {
	Machines machineCounts `json:"machines"`
	Jobs     jobCounts     `json:"jobs"`
	Services jobCounts     `json:"services"`
	Probes   jobCounts     `json:"probes"`
	// Les certificats vus par les sondes actives : combien, combien
	// approchent de leur fin, et la plus proche échéance.
	Certificates certificateCounts `json:"certificates"`
	// Les incidents ouverts sur la page de statut.
	Status statusCounts `json:"status"`
	// Les alertes ouvertes, et celles que personne n'a acquittées.
	Alerts alertCountsJSON `json:"alerts"`
}

type statusCounts struct {
	OpenIncidents int `json:"open_incidents"`
}

type certificateCounts struct {
	Total int `json:"total"`
	// Deux comptes disjoints : ce qui approche de sa fin, et ce qui l'a
	// déjà passée.
	Expiring int `json:"expiring"`
	Expired  int `json:"expired"`
	// La plus proche échéance à venir ; nulle quand il n'y en a aucune.
	SoonestExpiresAt *time.Time `json:"soonest_expires_at"`
}

type machineCounts struct {
	Total  int `json:"total"`
	Online int `json:"online"`
}

type jobCounts struct {
	Total     int `json:"total"`
	Attention int `json:"attention"`
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request) {
	languages := make([]string, 0, len(lang.Codes()))
	for _, code := range lang.Codes() {
		languages = append(languages, string(code))
	}
	s.writeAPI(w, http.StatusOK, sessionResponse{
		Version:      s.version,
		Language:     string(s.language()),
		Languages:    languages,
		Certificates: certificateThresholds{Warning: probe.CertificateWarning, Danger: probe.CertificateDanger},
	})
}

// Le commutateur de langue : mémorise le choix ; le front recharge son
// catalogue sans recharger la page.
func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	var request languageRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	code, ok := lang.Parse(request.Language)
	if !ok {
		s.apiRefuse(w, http.StatusBadRequest, "error.unknown_language")
		return
	}
	if err := s.saveLanguage(code); err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Le catalogue d'une langue, tel quel : les clés pointées et leurs chaînes.
// Les formats %s et %d restent au front, qui les remplit dans l'ordre.
func (s *Server) catalogAPI(w http.ResponseWriter, r *http.Request) {
	code, ok := lang.Parse(r.PathValue("code"))
	if !ok {
		s.apiNotFound(w)
		return
	}
	catalog := s.catalogs.For(code)
	strings := make(map[string]string, len(catalog.Keys()))
	for _, key := range catalog.Keys() {
		strings[key] = catalog.Get(key)
	}
	s.writeAPI(w, http.StatusOK, strings)
}

func (s *Server) counts(w http.ResponseWriter, r *http.Request) {
	total, online, err := s.machines.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	jobs, attention, err := s.heartbeats.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	services, failing, err := s.services.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	probes, down, err := s.probes.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	certificates, err := s.probes.Certificates(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	openIncidents, err := s.status.CountOpenIncidents(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	alerts, err := s.alerts.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, countsResponse{
		Machines:     machineCounts{Total: total, Online: online},
		Jobs:         jobCounts{Total: jobs, Attention: attention},
		Services:     jobCounts{Total: services, Attention: failing},
		Probes:       jobCounts{Total: probes, Attention: down},
		Certificates: certificatesToJSON(certificates),
		Status:       statusCounts{OpenIncidents: openIncidents},
		Alerts:       alertCountsJSON{Open: alerts.Open, Unacknowledged: alerts.Unacknowledged},
	})
}

func certificatesToJSON(counted probe.Certificates) certificateCounts {
	response := certificateCounts{Total: counted.Total, Expiring: counted.Expiring, Expired: counted.Expired}
	if !counted.Soonest.IsZero() {
		soonest := counted.Soonest.UTC()
		response.SoonestExpiresAt = &soonest
	}
	return response
}
