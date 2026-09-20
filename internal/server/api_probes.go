package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

const (
	// Les essais montrés sur la fiche, et la profondeur des barres
	// d'uptime : trente jours dans la liste, quatre-vingt-dix sur la fiche.
	probeResultsShown = 50
	probeListDays     = 30 * 24 * time.Hour
	probeDetailDays   = 90 * 24 * time.Hour
)

// Une sonde telle que le front la lit : des faits, des durées en secondes
// ou en millisecondes ; les libellés, les pastilles et les pourcentages se
// font dans le navigateur.
type probeJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Target      string `json:"target"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// Sans son flux, la machine ne sonde pas : l'état montré est le dernier
	// connu, et l'interface le dit plutôt que de le faire croire vrai.
	MachineOnline     bool             `json:"machine_online"`
	ServiceID         string           `json:"service_id"`
	ServiceName       string           `json:"service_name"`
	Status            string           `json:"status"`
	IntervalSeconds   int              `json:"interval_seconds"`
	TimeoutSeconds    int              `json:"timeout_seconds"`
	FailureThreshold  int              `json:"failure_threshold"`
	RecoveryThreshold int              `json:"recovery_threshold"`
	Method            string           `json:"method"`
	ExpectedStatus    string           `json:"expected_status"`
	ExpectedBody      string           `json:"expected_body"`
	FollowRedirects   bool             `json:"follow_redirects"`
	TLS               bool             `json:"tls"`
	LastCheckedAt     *time.Time       `json:"last_checked_at"`
	LastDurationMs    int64            `json:"last_duration_ms"`
	LastCode          *int             `json:"last_code"`
	LastReason        string           `json:"last_reason"`
	Certificate       *certificateJSON `json:"certificate"`
	CreatedAt         time.Time        `json:"created_at"`
}

// La chaîne présentée, réduite à des faits. Le serveur ne dit ni « valide »
// ni « à renouveler » : il donne les dates et les deux vérifications, le
// navigateur en tire l'état et la pastille.
type certificateJSON struct {
	Subject       string    `json:"subject"`
	Issuer        string    `json:"issuer"`
	NotBefore     time.Time `json:"not_before"`
	NotAfter      time.Time `json:"not_after"`
	Fingerprint   string    `json:"fingerprint"`
	ChainValid    bool      `json:"chain_valid"`
	HostnameMatch bool      `json:"hostname_match"`
	// Vide quand la cible n'agrafe rien : openCloud ne contacte aucun
	// répondeur pour le savoir.
	OCSP string `json:"ocsp"`
}

type resultJSON struct {
	CheckedAt  time.Time `json:"checked_at"`
	Outcome    string    `json:"outcome"`
	DurationMs int64     `json:"duration_ms"`
	Code       *int      `json:"code"`
	Reason     string    `json:"reason"`
}

// Un jour agrégé : des comptes, jamais un pourcentage, pour que la barre
// et les fenêtres s'additionnent dans le navigateur.
type dayJSON struct {
	Day        string `json:"day"`
	Total      int    `json:"total"`
	Success    int    `json:"success"`
	Degraded   int    `json:"degraded"`
	DurationMs int64  `json:"duration_ms"`
}

type uptimeJSON struct {
	Window  string `json:"window"`
	Total   int    `json:"total"`
	Success int    `json:"success"`
}

type probesResponse struct {
	Probes []probeJSON `json:"probes"`
	// Les jours de chaque sonde, par identifiant : une seule lecture sert
	// toutes les barres de la liste.
	Days map[string][]dayJSON `json:"days"`
}

type probeResponse struct {
	Probe   probeJSON    `json:"probe"`
	Uptime  []uptimeJSON `json:"uptime"`
	Days    []dayJSON    `json:"days"`
	Results []resultJSON `json:"results"`
}

type probeRequest struct {
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	Target            string `json:"target"`
	MachineID         string `json:"machine_id"`
	ServiceID         string `json:"service_id"`
	IntervalSeconds   int    `json:"interval_seconds"`
	TimeoutSeconds    int    `json:"timeout_seconds"`
	FailureThreshold  int    `json:"failure_threshold"`
	RecoveryThreshold int    `json:"recovery_threshold"`
	Method            string `json:"method"`
	ExpectedStatus    string `json:"expected_status"`
	ExpectedBody      string `json:"expected_body"`
	FollowRedirects   bool   `json:"follow_redirects"`
	TLS               bool   `json:"tls"`
}

func probeToJSON(found probe.Probe, online bool) probeJSON {
	return probeJSON{
		ID:                found.ID,
		Name:              found.Name,
		Kind:              string(found.Kind),
		Target:            found.Target,
		MachineID:         found.MachineID,
		MachineName:       found.MachineName,
		MachineOnline:     online,
		ServiceID:         found.ServiceID,
		ServiceName:       found.ServiceName,
		Status:            string(found.Status),
		IntervalSeconds:   int(found.Interval.Seconds()),
		TimeoutSeconds:    int(found.Timeout.Seconds()),
		FailureThreshold:  found.FailureThreshold,
		RecoveryThreshold: found.RecoveryThreshold,
		Method:            found.Method,
		ExpectedStatus:    found.ExpectedStatus,
		ExpectedBody:      found.ExpectedBody,
		FollowRedirects:   found.FollowRedirects,
		TLS:               found.TLS,
		LastCheckedAt:     timeOrNil(found.LastCheckedAt),
		LastDurationMs:    found.LastDurationMs,
		LastCode:          found.LastCode,
		LastReason:        string(found.LastReason),
		Certificate:       certificateToJSON(found.Certificate),
		CreatedAt:         found.CreatedAt.UTC(),
	}
}

func certificateToJSON(certificate *probe.Certificate) *certificateJSON {
	if certificate == nil {
		return nil
	}
	return &certificateJSON{
		Subject:       certificate.Subject,
		Issuer:        certificate.Issuer,
		NotBefore:     certificate.NotBefore.UTC(),
		NotAfter:      certificate.NotAfter.UTC(),
		Fingerprint:   certificate.Fingerprint,
		ChainValid:    certificate.ChainValid,
		HostnameMatch: certificate.HostnameMatch,
		OCSP:          string(certificate.OCSP),
	}
}

func dayToJSON(day probe.Day) dayJSON {
	return dayJSON{
		Day:        day.Day.UTC().Format(time.DateOnly),
		Total:      day.Total,
		Success:    day.Success,
		Degraded:   day.Degraded,
		DurationMs: day.DurationMs,
	}
}

func resultToJSON(result probe.Result) resultJSON {
	return resultJSON{
		CheckedAt:  result.CheckedAt.UTC(),
		Outcome:    string(result.Outcome),
		DurationMs: result.DurationMs,
		Code:       result.Code,
		Reason:     string(result.Reason),
	}
}

// onlineMachines dit, par machine, si son flux est ouvert : c'est ce qui
// permet à l'interface de distinguer « hors ligne » de « plus personne ne
// sonde ».
func (s *Server) onlineMachines(r *http.Request) (map[string]bool, error) {
	machines, err := s.machines.List(r.Context())
	if err != nil {
		return nil, err
	}
	online := make(map[string]bool, len(machines))
	for _, status := range machines {
		online[status.ID] = status.Online
	}
	return online, nil
}

func (s *Server) listProbes(w http.ResponseWriter, r *http.Request) {
	s.writeProbeList(w, r, "")
}

func (s *Server) listMachineProbes(w http.ResponseWriter, r *http.Request) {
	if _, err := s.machines.Get(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, machine.ErrNotFound) {
			s.apiNotFound(w)
			return
		}
		s.apiInternalError(w, r, err)
		return
	}
	s.writeProbeList(w, r, r.PathValue("id"))
}

// La liste porte les jours de chaque sonde : la barre d'uptime de la page
// ne demande pas une lecture par ligne.
func (s *Server) writeProbeList(w http.ResponseWriter, r *http.Request, machineID string) {
	probes, err := s.probes.List(r.Context(), machineID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	online, err := s.onlineMachines(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	days, err := s.probes.Days(r.Context(), "", probeListDays)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := probesResponse{Probes: make([]probeJSON, 0, len(probes)), Days: map[string][]dayJSON{}}
	carried := make(map[string]bool, len(probes))
	for _, found := range probes {
		carried[found.ID] = true
		response.Probes = append(response.Probes, probeToJSON(found, online[found.MachineID]))
	}
	for _, day := range days {
		if carried[day.ProbeID] {
			response.Days[day.ProbeID] = append(response.Days[day.ProbeID], dayToJSON(day))
		}
	}
	s.writeAPI(w, http.StatusOK, response)
}

// La page d'une sonde : ses faits, son uptime par fenêtre, ses jours et
// ses derniers essais.
func (s *Server) getProbe(w http.ResponseWriter, r *http.Request) {
	found, err := s.probes.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, probe.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	online, err := s.onlineMachines(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	uptimes, err := s.probes.Uptimes(r.Context(), found.ID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	days, err := s.probes.Days(r.Context(), found.ID, probeDetailDays)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	results, err := s.probes.Results(r.Context(), found.ID, probeResultsShown)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := probeResponse{
		Probe:   probeToJSON(found, online[found.MachineID]),
		Uptime:  make([]uptimeJSON, 0, len(uptimes)),
		Days:    make([]dayJSON, 0, len(days)),
		Results: make([]resultJSON, 0, len(results)),
	}
	for _, uptime := range uptimes {
		response.Uptime = append(response.Uptime, uptimeJSON{Window: uptime.Window, Total: uptime.Total, Success: uptime.Success})
	}
	for _, day := range days {
		response.Days = append(response.Days, dayToJSON(day))
	}
	for _, result := range results {
		response.Results = append(response.Results, resultToJSON(result))
	}
	s.writeAPI(w, http.StatusOK, response)
}

// L'opérateur nomme la sonde, dit ce qu'elle vise et quelle machine la
// porte ; le composant valide et répond par la clé du refus.
func (s *Server) createProbe(w http.ResponseWriter, r *http.Request) {
	var request probeRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	if _, err := s.machines.Get(r.Context(), request.MachineID); err != nil {
		if errors.Is(err, machine.ErrNotFound) {
			s.apiRefuse(w, http.StatusUnprocessableEntity, "probe.machine_invalid")
			return
		}
		s.apiInternalError(w, r, err)
		return
	}
	created, err := s.probes.Create(r.Context(), probe.Definition{
		Name:              request.Name,
		Kind:              probe.Kind(request.Kind),
		Target:            request.Target,
		MachineID:         request.MachineID,
		ServiceID:         request.ServiceID,
		Interval:          time.Duration(request.IntervalSeconds) * time.Second,
		Timeout:           time.Duration(request.TimeoutSeconds) * time.Second,
		FailureThreshold:  request.FailureThreshold,
		RecoveryThreshold: request.RecoveryThreshold,
		Method:            request.Method,
		ExpectedStatus:    request.ExpectedStatus,
		ExpectedBody:      request.ExpectedBody,
		FollowRedirects:   request.FollowRedirects,
		TLS:               request.TLS,
	})
	if key, refused := probeRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	online, err := s.onlineMachines(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusCreated, probeToJSON(created, online[created.MachineID]))
}

func probeRefusalKey(err error) (string, bool) {
	switch {
	case errors.Is(err, probe.ErrNameInvalid):
		return "probe.name_invalid", true
	case errors.Is(err, probe.ErrKindInvalid):
		return "probe.kind_invalid", true
	case errors.Is(err, probe.ErrTargetInvalid):
		return "probe.target_invalid", true
	case errors.Is(err, probe.ErrTargetForbidden):
		return "probe.target_forbidden", true
	case errors.Is(err, probe.ErrMachineInvalid):
		return "probe.machine_invalid", true
	case errors.Is(err, probe.ErrIntervalInvalid):
		return "probe.interval_invalid", true
	case errors.Is(err, probe.ErrTimeoutInvalid):
		return "probe.timeout_invalid", true
	case errors.Is(err, probe.ErrThresholdInvalid):
		return "probe.threshold_invalid", true
	case errors.Is(err, probe.ErrMethodInvalid):
		return "probe.method_invalid", true
	case errors.Is(err, probe.ErrStatusInvalid):
		return "probe.status_invalid", true
	case errors.Is(err, probe.ErrBodyInvalid):
		return "probe.body_invalid", true
	case errors.Is(err, probe.ErrTooMany):
		return "probe.too_many", true
	}
	return "", false
}

func (s *Server) deleteProbe(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.probes.Delete(r.Context(), r.PathValue("id")), probe.ErrNotFound)
}

func (s *Server) pauseProbe(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.probes.Pause(r.Context(), r.PathValue("id")), probe.ErrNotFound)
}

func (s *Server) resumeProbe(w http.ResponseWriter, r *http.Request) {
	err := s.probes.Resume(r.Context(), r.PathValue("id"))
	if errors.Is(err, probe.ErrNotPaused) {
		s.apiRefuse(w, http.StatusConflict, "probe.not_paused")
		return
	}
	s.finishAPIAction(w, r, err, probe.ErrNotFound)
}
