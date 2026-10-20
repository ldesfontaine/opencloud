package server

import (
	"context"
	"errors"
	"net/http"
	"time"

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
	MachineOnline bool   `json:"machine_online"`
	ServiceID     string `json:"service_id"`
	ServiceName   string `json:"service_name"`
	// La machine qui porte le service : la sonde peut en voir un d'une autre.
	ServiceMachineName string           `json:"service_machine_name"`
	Status             string           `json:"status"`
	IntervalSeconds    int              `json:"interval_seconds"`
	TimeoutSeconds     int              `json:"timeout_seconds"`
	FailureThreshold   int              `json:"failure_threshold"`
	RecoveryThreshold  int              `json:"recovery_threshold"`
	Method             string           `json:"method"`
	ExpectedStatus     string           `json:"expected_status"`
	ExpectedBody       string           `json:"expected_body"`
	FollowRedirects    bool             `json:"follow_redirects"`
	TLS                bool             `json:"tls"`
	LastCheckedAt      *time.Time       `json:"last_checked_at"`
	LastDurationMs     int64            `json:"last_duration_ms"`
	LastCode           *int             `json:"last_code"`
	LastReason         string           `json:"last_reason"`
	Certificate        *certificateJSON `json:"certificate"`
	CreatedAt          time.Time        `json:"created_at"`
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
		ID:                 found.ID,
		Name:               found.Name,
		Kind:               string(found.Kind),
		Target:             found.Target,
		MachineID:          found.MachineID,
		MachineName:        found.MachineName,
		MachineOnline:      online,
		ServiceID:          found.ServiceID,
		ServiceName:        found.ServiceName,
		ServiceMachineName: found.ServiceMachineName,
		Status:             string(found.Status),
		IntervalSeconds:    int(found.Interval.Seconds()),
		TimeoutSeconds:     int(found.Timeout.Seconds()),
		FailureThreshold:   found.FailureThreshold,
		RecoveryThreshold:  found.RecoveryThreshold,
		Method:             found.Method,
		ExpectedStatus:     found.ExpectedStatus,
		ExpectedBody:       found.ExpectedBody,
		FollowRedirects:    found.FollowRedirects,
		TLS:                found.TLS,
		LastCheckedAt:      timeOrNil(found.LastCheckedAt),
		LastDurationMs:     found.LastDurationMs,
		LastCode:           found.LastCode,
		LastReason:         string(found.LastReason),
		Certificate:        certificateToJSON(found.Certificate),
		CreatedAt:          found.CreatedAt.UTC(),
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
func (s *Server) onlineMachines(ctx context.Context) (map[string]bool, error) {
	machines, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	online := make(map[string]bool, len(machines))
	for _, machineStatus := range machines {
		online[machineStatus.ID] = machineStatus.Online
	}
	return online, nil
}

func (s *Server) listProbes(w http.ResponseWriter, r *http.Request) {
	s.writeProbeList(w, r, "")
}

func (s *Server) listMachineProbes(w http.ResponseWriter, r *http.Request) {
	if !s.machineExists(w, r, r.PathValue("id")) {
		return
	}
	s.writeProbeList(w, r, r.PathValue("id"))
}

// La liste porte les jours de chaque sonde : la barre d'uptime de la page
// ne demande pas une lecture par ligne.
func (s *Server) writeProbeList(w http.ResponseWriter, r *http.Request, machineID string) {
	response, err := s.probeListOf(r.Context(), machineID)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

// probeListOf assemble les sondes d'une machine, ou de toutes quand
// machineID est vide, avec leurs jours ; l'API et l'outil MCP la rendent
// telle quelle, l'outil la pagine et masque les cibles après.
func (s *Server) probeListOf(ctx context.Context, machineID string) (probesResponse, error) {
	probes, err := s.probes.List(ctx, machineID)
	if err != nil {
		return probesResponse{}, err
	}
	online, err := s.onlineMachines(ctx)
	if err != nil {
		return probesResponse{}, err
	}
	days, err := s.probes.Days(ctx, "", probeListDays)
	if err != nil {
		return probesResponse{}, err
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
	return response, nil
}

// La page d'une sonde : ses faits, son uptime par fenêtre, ses jours et
// ses derniers essais.
func (s *Server) getProbe(w http.ResponseWriter, r *http.Request) {
	found, err := s.probes.Get(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, probe.ErrNotFound) {
		return
	}
	response, err := s.probeResponseOf(r.Context(), found)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

// probeResponseOf assemble la fiche d'une sonde ; l'API et l'outil MCP la
// rendent telle quelle.
func (s *Server) probeResponseOf(ctx context.Context, found probe.Probe) (probeResponse, error) {
	online, err := s.onlineMachines(ctx)
	if err != nil {
		return probeResponse{}, err
	}
	uptimes, err := s.probes.Uptimes(ctx, found.ID)
	if err != nil {
		return probeResponse{}, err
	}
	days, err := s.probes.Days(ctx, found.ID, probeDetailDays)
	if err != nil {
		return probeResponse{}, err
	}
	results, err := s.probes.Results(ctx, found.ID, probeResultsShown)
	if err != nil {
		return probeResponse{}, err
	}
	response := probeResponse{
		Probe: probeToJSON(found, online[found.MachineID]),
		Uptime: convertAll(uptimes, func(uptime probe.Uptime) uptimeJSON {
			return uptimeJSON{Window: uptime.Window, Total: uptime.Total, Success: uptime.Success}
		}),
		Days:    convertAll(days, dayToJSON),
		Results: convertAll(results, resultToJSON),
	}
	return response, nil
}

// L'opérateur nomme la sonde, dit ce qu'elle vise et quelle machine la
// porte ; le composant valide et répond par la clé du refus.
func (s *Server) createProbe(w http.ResponseWriter, r *http.Request) {
	var request probeRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.probes.Create(r.Context(), probeDefinition(request))
	if s.failed(w, r, err, nil) {
		return
	}
	online, err := s.onlineMachines(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, probeToJSON(created, online[created.MachineID]))
}

// L'édition remplace la définition entière, comme la création la donne :
// un champ omis prend sa valeur par défaut. Le composant dit ce que
// l'édition remet à zéro ; l'identifiant et l'histoire restent.
func (s *Server) updateProbe(w http.ResponseWriter, r *http.Request) {
	var request probeRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	updated, err := s.probes.Update(r.Context(), r.PathValue("id"), probeDefinition(request))
	if s.failed(w, r, err, probe.ErrNotFound) {
		return
	}
	online, err := s.onlineMachines(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, probeToJSON(updated, online[updated.MachineID]))
}

func probeDefinition(request probeRequest) probe.Definition {
	return probe.Definition{
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
	}
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
