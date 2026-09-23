package server

import (
	"context"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
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
	// Les pourcentages à partir desquels un volume alerte, puis s'aggrave :
	// la phrase de l'alerte les cite, dans le navigateur comme au canal.
	Disks diskThresholds `json:"disk_thresholds"`
}

type diskThresholds struct {
	Attention int `json:"attention"`
	Danger    int `json:"danger"`
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
	Services serviceCounts `json:"services"`
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

// Les services : ceux qui vont mal, et ceux qui ont une image plus
// récente à tirer, hors épinglés.
type serviceCounts struct {
	Total     int `json:"total"`
	Attention int `json:"attention"`
	Updates   int `json:"updates"`
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
		Disks:        diskThresholds{Attention: alert.DiskAttentionPercent, Danger: alert.DiskDangerPercent},
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
	response, err := s.countsOf(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

// countsOf assemble les compteurs ; l'API et l'outil MCP les rendent tels quels.
func (s *Server) countsOf(ctx context.Context) (countsResponse, error) {
	total, online, err := s.machines.Count(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	jobs, attention, err := s.heartbeats.Count(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	services, failing, err := s.services.Count(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	probes, down, err := s.probes.Count(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	certificates, err := s.probes.Certificates(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	updates, err := s.updates.Count(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	openIncidents, err := s.status.CountOpenIncidents(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	alerts, err := s.alerts.Count(ctx)
	if err != nil {
		return countsResponse{}, err
	}
	return countsResponse{
		Machines:     machineCounts{Total: total, Online: online},
		Jobs:         jobCounts{Total: jobs, Attention: attention},
		Services:     serviceCounts{Total: services, Attention: failing, Updates: updates},
		Probes:       jobCounts{Total: probes, Attention: down},
		Certificates: certificatesToJSON(certificates),
		Status:       statusCounts{OpenIncidents: openIncidents},
		Alerts:       alertCountsJSON{Open: alerts.Open, Unacknowledged: alerts.Unacknowledged},
	}, nil
}

func certificatesToJSON(counted probe.Certificates) certificateCounts {
	response := certificateCounts{Total: counted.Total, Expiring: counted.Expiring, Expired: counted.Expired}
	if !counted.Soonest.IsZero() {
		soonest := counted.Soonest.UTC()
		response.SoonestExpiresAt = &soonest
	}
	return response
}
