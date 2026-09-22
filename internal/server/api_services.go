package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

const (
	defaultTransitions = 50
	maxTransitions     = 500
	// Les suivis de journaux ouverts par des onglets, en tout : chaque
	// suivi est une seconde connexion, hors du plafond du direct.
	maxLogStreams = 16
	codeLogsBusy  = "service.logs_busy"
)

// Un service tel que le front le lit : des faits, jamais une pastille. Le
// front dérive Actif, Défaillant, Arrêté de l'état, du code de sortie et
// de la santé. La mesure courante est là si elle est fraîche ; les
// constats d'exposition sont calculés à la lecture.
type serviceJSON struct {
	ID          string `json:"id"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Group       string `json:"group"`
	ContainerID string `json:"container_id"`
	Image       string `json:"image"`
	ImageID     string `json:"image_id"`
	// Ce que Compose dit du service, de quoi lire la commande fabriquée.
	ComposeService string `json:"compose_service"`
	ComposeDir     string `json:"compose_dir"`
	ComposeFile    string `json:"compose_file"`
	// Ce que l'opérateur veut des mises à jour : "", pinned, excluded.
	UpdatePolicy string `json:"update_policy"`
	// Le dernier constat sur l'image ; absent tant que l'agent n'a rien dit.
	ImageCheck   *imageCheckJSON      `json:"image_check"`
	State        string               `json:"state"`
	ExitCode     int                  `json:"exit_code"`
	Health       string               `json:"health"`
	RestartCount int                  `json:"restart_count"`
	Ports        []service.Port       `json:"ports"`
	NetworkMode  string               `json:"network_mode"`
	Privileged   bool                 `json:"privileged"`
	Networks     []service.Attachment `json:"networks"`
	DependsOn    []service.Dependency `json:"depends_on"`
	Exposure     []service.Finding    `json:"exposure"`
	CreatedAt    time.Time            `json:"created_at"`
	StartedAt    *time.Time           `json:"started_at"`
	FinishedAt   *time.Time           `json:"finished_at"`
	FirstSeenAt  time.Time            `json:"first_seen_at"`
	LastSeenAt   time.Time            `json:"last_seen_at"`
	ArchivedAt   *time.Time           `json:"archived_at"`
	Current      *sampleJSON          `json:"current"`
}

// Le constat de l'agent sur l'image, et ce que le serveur en fait : le
// type de mise à jour, l'image à tirer, la commande à copier, jamais
// exécutée. Une pastille se dérive de kind ; vide, l'image est à jour.
type imageCheckJSON struct {
	CheckedAt    time.Time `json:"checked_at"`
	Outcome      string    `json:"outcome"`
	LocalDigest  string    `json:"local_digest"`
	RemoteDigest string    `json:"remote_digest"`
	NewerTag     string    `json:"newer_tag"`
	NewerDigest  string    `json:"newer_digest"`
	Kind         string    `json:"kind"`
	Target       string    `json:"target"`
	Command      string    `json:"command"`
}

type sampleJSON struct {
	SampledAt  time.Time `json:"sampled_at"`
	CPUPercent float64   `json:"cpu_percent"`
	MemUsed    int64     `json:"mem_used"`
	MemLimit   int64     `json:"mem_limit"`
}

// Ce que la machine dit de son Docker ; absent tant qu'elle n'a rien dit.
type engineJSON struct {
	MachineID  string    `json:"machine_id"`
	Present    bool      `json:"present"`
	Reason     string    `json:"reason"`
	Version    string    `json:"version"`
	APIVersion string    `json:"api_version"`
	CheckedAt  time.Time `json:"checked_at"`
}

type servicesResponse struct {
	Services []serviceJSON `json:"services"`
	Engines  []engineJSON  `json:"engines"`
}

type transitionJSON struct {
	ID             int64     `json:"id"`
	At             time.Time `json:"at"`
	Action         string    `json:"action"`
	PreviousState  string    `json:"previous_state"`
	NewState       string    `json:"new_state"`
	PreviousHealth string    `json:"previous_health"`
	NewHealth      string    `json:"new_health"`
	ExitCode       *int      `json:"exit_code"`
	Replayed       bool      `json:"replayed"`
	Snippet        string    `json:"snippet"`
}

type serviceResponse struct {
	Service     serviceJSON      `json:"service"`
	Engine      *engineJSON      `json:"engine"`
	Transitions []transitionJSON `json:"transitions"`
}

type transitionsResponse struct {
	Transitions []transitionJSON `json:"transitions"`
}

type logsResponse struct {
	Lines []logLineJSON `json:"lines"`
}

type logLineJSON struct {
	At     *time.Time `json:"at"`
	Stream string     `json:"stream"`
	Text   string     `json:"text"`
}

// listServices rend tous les services vivants, avec la machine de chacun
// et l'état du Docker de chaque machine.
func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	s.writeServices(w, r, "")
}

func (s *Server) listMachineServices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	s.writeServices(w, r, id)
}

func (s *Server) writeServices(w http.ResponseWriter, r *http.Request, machineID string) {
	services, err := s.services.List(r.Context(), machineID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	names, err := s.machineNames(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	currents, err := s.currentByService(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	engines, err := s.services.Engines(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	checks, err := s.updates.Checks(r.Context(), machineID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := servicesResponse{Services: make([]serviceJSON, 0, len(services)), Engines: []engineJSON{}}
	for _, item := range services {
		response.Services = append(response.Services, serviceToJSON(item, names[item.MachineID], currents[item.ID], checks))
	}
	for _, engine := range engines {
		if machineID == "" || engine.MachineID == machineID {
			response.Engines = append(response.Engines, engineToJSON(engine))
		}
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getService(w http.ResponseWriter, r *http.Request) {
	item, ok := s.serviceOr404(w, r)
	if !ok {
		return
	}
	names, err := s.machineNames(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	currents, err := s.currentByService(r)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	transitions, err := s.services.Transitions(r.Context(), item.ID, defaultTransitions)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	engines, err := s.services.Engines(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	checks, err := s.updates.Checks(r.Context(), item.MachineID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := serviceResponse{Service: serviceToJSON(item, names[item.MachineID], currents[item.ID], checks), Transitions: transitionsToJSON(transitions)}
	for _, engine := range engines {
		if engine.MachineID == item.MachineID {
			converted := engineToJSON(engine)
			response.Engine = &converted
		}
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) listServiceTransitions(w http.ResponseWriter, r *http.Request) {
	item, ok := s.serviceOr404(w, r)
	if !ok {
		return
	}
	limit := defaultTransitions
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, maxTransitions)
	}
	transitions, err := s.services.Transitions(r.Context(), item.ID, limit)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, transitionsResponse{Transitions: transitionsToJSON(transitions)})
}

// getServiceLogs tire les dernières lignes en un coup ; le repli du direct.
func (s *Server) getServiceLogs(w http.ResponseWriter, r *http.Request) {
	item, ok := s.serviceOr404(w, r)
	if !ok {
		return
	}
	lines, err := s.services.FetchLogs(r.Context(), item.ID, tailOf(r))
	if err != nil {
		s.writeLogsError(w, r, err)
		return
	}
	response := logsResponse{Lines: make([]logLineJSON, 0, len(lines))}
	for _, line := range lines {
		response.Lines = append(response.Lines, logLineToJSON(line))
	}
	s.writeAPI(w, http.StatusOK, response)
}

// streamServiceLogs suit le journal en SSE : un événement « line » par
// ligne, « end » quand le journal ou la machine s'arrête. C'est la seule
// seconde connexion qu'un onglet ouvre, plafonnée à part.
func (s *Server) streamServiceLogs(w http.ResponseWriter, r *http.Request) {
	item, ok := s.serviceOr404(w, r)
	if !ok {
		return
	}
	if s.logStreams.Load() >= maxLogStreams {
		s.writeAPIError(w, http.StatusServiceUnavailable, codeLogsBusy)
		return
	}
	s.logStreams.Add(1)
	defer s.logStreams.Add(-1)
	batches, err := s.services.FollowLogs(r.Context(), item.ID, tailOf(r))
	if err != nil {
		s.writeLogsError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if err := writeComment(w, "open"); err != nil {
		return
	}
	ticker := time.NewTicker(eventsPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if err := writeComment(w, "ping"); err != nil {
				return
			}
		case batch, open := <-batches:
			if !open {
				_ = writeJSONEvent(w, "end", map[string]string{})
				return
			}
			for _, line := range batch.Lines {
				if err := writeJSONEvent(w, "line", logLineToJSON(line)); err != nil {
					return
				}
			}
			if batch.Done || batch.Error != "" {
				_ = writeJSONEvent(w, "end", map[string]string{"error": batch.Error})
				return
			}
		}
	}
}

// writeJSONEvent pousse un événement SSE dont le contenu est du JSON sur
// une ligne : une ligne de journal est du texte, jamais de balises.
func writeJSONEvent(w http.ResponseWriter, name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil { // #nosec G705 -- text/event-stream, JSON encodé.
		return err
	}
	return http.NewResponseController(w).Flush()
}

func (s *Server) writeLogsError(w http.ResponseWriter, r *http.Request, err error) {
	var failed *service.LogsError
	switch {
	case errors.Is(err, service.ErrMachineOffline):
		s.apiRefuse(w, http.StatusServiceUnavailable, "service.machine_offline")
	case errors.Is(err, service.ErrLogsBusy):
		s.apiRefuse(w, http.StatusTooManyRequests, codeLogsBusy)
	case errors.Is(err, service.ErrLogsTimeout):
		s.apiRefuse(w, http.StatusGatewayTimeout, "service.logs_timeout")
	case errors.As(err, &failed):
		s.apiRefuse(w, http.StatusBadGateway, failed.Code)
	default:
		s.apiInternalError(w, r, err)
	}
}

func (s *Server) serviceOr404(w http.ResponseWriter, r *http.Request) (service.Service, bool) {
	item, err := s.services.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, service.ErrNotFound) {
		s.apiNotFound(w)
		return service.Service{}, false
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return service.Service{}, false
	}
	return item, true
}

func (s *Server) machineNames(r *http.Request) (map[string]string, error) {
	statuses, err := s.machines.List(r.Context())
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(statuses))
	for _, status := range statuses {
		names[status.ID] = status.Name
	}
	return names, nil
}

func (s *Server) currentByService(r *http.Request) (map[string]service.Current, error) {
	currents, err := s.services.CurrentAll(r.Context())
	if err != nil {
		return nil, err
	}
	byService := make(map[string]service.Current, len(currents))
	for _, current := range currents {
		byService[current.ServiceID] = current
	}
	return byService, nil
}

func tailOf(r *http.Request) int {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	return tail
}

func serviceToJSON(item service.Service, machineName string, current service.Current, checks map[string]update.Check) serviceJSON {
	converted := serviceJSON{
		ID: item.ID, MachineID: item.MachineID, MachineName: machineName, Kind: string(item.Kind),
		Name: item.Name, Group: item.Group, ContainerID: item.ContainerID, Image: item.Image, ImageID: item.ImageID,
		ComposeService: item.ComposeService, ComposeDir: item.ComposeDir, ComposeFile: item.ComposeFile,
		UpdatePolicy: string(item.UpdatePolicy), ImageCheck: imageCheckToJSON(item, checks),
		State: string(item.State), ExitCode: item.ExitCode, Health: string(item.Health), RestartCount: item.RestartCount,
		Ports: orEmpty(item.Ports), NetworkMode: item.NetworkMode, Privileged: item.Privileged,
		Networks: orEmpty(item.Networks), DependsOn: orEmpty(item.DependsOn), Exposure: service.Exposure(item),
		CreatedAt: item.CreatedAt.UTC(), StartedAt: timeOrNil(item.StartedAt), FinishedAt: timeOrNil(item.FinishedAt),
		FirstSeenAt: item.FirstSeenAt.UTC(), LastSeenAt: item.LastSeenAt.UTC(), ArchivedAt: timeOrNil(item.ArchivedAt),
	}
	for i := range converted.Networks {
		converted.Networks[i].Aliases = orEmpty(converted.Networks[i].Aliases)
	}
	if current.Available && current.Sample != nil {
		converted.Current = &sampleJSON{
			SampledAt: current.Sample.SampledAt.UTC(), CPUPercent: current.Sample.CPUPercent,
			MemUsed: current.Sample.MemUsed, MemLimit: current.Sample.MemLimit,
		}
	}
	return converted
}

func imageCheckToJSON(item service.Service, checks map[string]update.Check) *imageCheckJSON {
	check, ok := update.CheckFor(checks, item)
	if !ok {
		return nil
	}
	converted := &imageCheckJSON{
		CheckedAt: check.CheckedAt.UTC(), Outcome: string(check.Outcome),
		LocalDigest: check.LocalDigest, RemoteDigest: check.RemoteDigest,
		NewerTag: check.NewerTag, NewerDigest: check.NewerDigest, Kind: string(check.Kind),
	}
	if check.HasUpdate() {
		converted.Target = check.Target()
		converted.Command = update.Command(item, check)
	}
	return converted
}

// Une liste nulle s'écrit [] : le front n'a pas à distinguer null de vide.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func engineToJSON(engine service.Engine) engineJSON {
	return engineJSON{
		MachineID: engine.MachineID, Present: engine.Present, Reason: string(engine.Reason),
		Version: engine.Version, APIVersion: engine.APIVersion, CheckedAt: engine.CheckedAt.UTC(),
	}
}

func transitionsToJSON(transitions []service.Transition) []transitionJSON {
	converted := make([]transitionJSON, 0, len(transitions))
	for _, t := range transitions {
		converted = append(converted, transitionJSON{
			ID: t.ID, At: t.At.UTC(), Action: t.Action, PreviousState: string(t.PreviousState), NewState: string(t.NewState),
			PreviousHealth: string(t.PreviousHealth), NewHealth: string(t.NewHealth), ExitCode: t.ExitCode, Replayed: t.Replayed, Snippet: t.Snippet,
		})
	}
	return converted
}

func logLineToJSON(line service.LogLine) logLineJSON {
	return logLineJSON{At: timeOrNil(line.At), Stream: line.Stream, Text: line.Text}
}
