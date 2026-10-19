package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/update"
)

const (
	defaultTransitions = 50
	maxTransitions     = 500
	// Les lectures de journaux en cours, en tout : suivis des onglets,
	// tirages uniques et outil MCP. Chaque suivi est une seconde connexion,
	// hors du plafond du direct.
	maxLogStreams = 16
	// Le code que l'agent rend aussi (dockerwatch.LogsBusy) : un agent déjà
	// installé garde son nom, le catalogue le traduit sous cette clé.
	codeLogsBusy    = "service.logs_busy"
	codeMachineBusy = "container.machine_busy"
)

// Un conteneur tel que le front le lit : des faits, jamais une pastille. Le
// front dérive Actif, Défaillant, Arrêté de l'état, du code de sortie et
// de la santé. La mesure courante est là si elle est fraîche ; les
// constats d'exposition sont calculés à la lecture.
type containerJSON struct {
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
	// Ce que l'opérateur veut des mises à jour, réglé sur l'unité de la
	// fiche : watch, hold, ignore. En lecture seule ici ; watch sans unité.
	UpdatePolicy string `json:"update_policy"`
	// Le service de l'annuaire qui porte l'unité de la fiche ; vide : non
	// déclaré.
	DeclaredServiceID string `json:"declared_service_id"`
	// Le dernier constat sur l'image ; absent tant que l'agent n'a rien dit.
	ImageCheck *imageCheckJSON `json:"image_check"`
	// Le support de l'image, sur la fiche seulement, et seulement quand
	// l'opérateur a activé la fin de support.
	Lifecycle    *lifecycleJSON         `json:"lifecycle,omitempty"`
	State        string                 `json:"state"`
	ExitCode     int                    `json:"exit_code"`
	OOMKilled    bool                   `json:"oom_killed"`
	Killed       bool                   `json:"killed"`
	Health       string                 `json:"health"`
	RestartCount int                    `json:"restart_count"`
	Ports        []container.Port       `json:"ports"`
	NetworkMode  string                 `json:"network_mode"`
	Privileged   bool                   `json:"privileged"`
	Networks     []container.Attachment `json:"networks"`
	DependsOn    []container.Dependency `json:"depends_on"`
	Exposure     []container.Finding    `json:"exposure"`
	CreatedAt    time.Time              `json:"created_at"`
	StartedAt    *time.Time             `json:"started_at"`
	FinishedAt   *time.Time             `json:"finished_at"`
	FirstSeenAt  time.Time              `json:"first_seen_at"`
	LastSeenAt   time.Time              `json:"last_seen_at"`
	ArchivedAt   *time.Time             `json:"archived_at"`
	Current      *sampleJSON            `json:"current"`
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

type containersResponse struct {
	Containers []containerJSON `json:"containers"`
	Engines    []engineJSON    `json:"engines"`
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
	OOMKilled      bool      `json:"oom_killed"`
	Killed         bool      `json:"killed"`
	Replayed       bool      `json:"replayed"`
	Snippet        string    `json:"snippet"`
}

type containerResponse struct {
	Container containerJSON `json:"container"`
	Engine    *engineJSON   `json:"engine"`
	// Ce que l'annuaire dit du conteneur ; sur la fiche seulement, pas dans
	// les listes.
	Directory   *containerDirectoryJSON `json:"directory"`
	Transitions []transitionJSON        `json:"transitions"`
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

// listContainers rend tous les conteneurs vivants, avec la machine de chacun
// et l'état du Docker de chaque machine.
func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	s.writeContainers(w, r, "")
}

func (s *Server) listMachineContainers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	s.writeContainers(w, r, id)
}

func (s *Server) writeContainers(w http.ResponseWriter, r *http.Request, machineID string) {
	containers, err := s.containers.List(r.Context(), machineID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	names, err := s.machineNames(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	currents, err := s.currentByContainer(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	engines, err := s.containers.Engines(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	checks, err := s.updates.Checks(r.Context(), machineID)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := containersResponse{Containers: make([]containerJSON, 0, len(containers)), Engines: []engineJSON{}}
	for _, item := range containers {
		response.Containers = append(response.Containers, containerToJSON(item, names[item.MachineID], currents[item.ID], checks))
	}
	for _, engine := range engines {
		if machineID == "" || engine.MachineID == machineID {
			response.Engines = append(response.Engines, engineToJSON(engine))
		}
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getContainer(w http.ResponseWriter, r *http.Request) {
	item, ok := s.containerOr404(w, r)
	if !ok {
		return
	}
	response, err := s.containerResponseOf(r.Context(), item)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

// containerResponseOf assemble la fiche d'un conteneur ; l'API et l'outil MCP
// la rendent telle quelle.
func (s *Server) containerResponseOf(ctx context.Context, item container.Container) (containerResponse, error) {
	names, err := s.machineNames(ctx)
	if err != nil {
		return containerResponse{}, err
	}
	currents, err := s.currentByContainer(ctx)
	if err != nil {
		return containerResponse{}, err
	}
	transitions, err := s.containers.Transitions(ctx, item.ID, defaultTransitions)
	if err != nil {
		return containerResponse{}, err
	}
	engines, err := s.containers.Engines(ctx)
	if err != nil {
		return containerResponse{}, err
	}
	checks, err := s.updates.Checks(ctx, item.MachineID)
	if err != nil {
		return containerResponse{}, err
	}
	response := containerResponse{Container: containerToJSON(item, names[item.MachineID], currents[item.ID], checks), Transitions: transitionsToJSON(transitions)}
	response.Container.Lifecycle, err = s.lifecycleOf(ctx, item.Image)
	if err != nil {
		return containerResponse{}, err
	}
	response.Directory, err = s.containerDirectoryOf(ctx, item)
	if err != nil {
		return containerResponse{}, err
	}
	for _, engine := range engines {
		if engine.MachineID == item.MachineID {
			converted := engineToJSON(engine)
			response.Engine = &converted
		}
	}
	return response, nil
}

func (s *Server) listContainerTransitions(w http.ResponseWriter, r *http.Request) {
	item, ok := s.containerOr404(w, r)
	if !ok {
		return
	}
	limit := defaultTransitions
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 {
		limit = min(value, maxTransitions)
	}
	transitions, err := s.containers.Transitions(r.Context(), item.ID, limit)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, transitionsResponse{Transitions: transitionsToJSON(transitions)})
}

// getContainerLogs tire les dernières lignes en un coup ; le repli du direct.
func (s *Server) getContainerLogs(w http.ResponseWriter, r *http.Request) {
	item, ok := s.containerOr404(w, r)
	if !ok {
		return
	}
	if !s.takeLogSlot() {
		s.apiRefuse(w, http.StatusServiceUnavailable, codeLogsBusy)
		return
	}
	defer s.releaseLogSlot()
	lines, err := s.containers.FetchLogs(r.Context(), item.ID, tailOf(r))
	if err != nil {
		s.writeLogsError(w, r, err)
		return
	}
	response := logsResponse{Lines: convertAll(lines, logLineToJSON)}
	s.writeAPI(w, http.StatusOK, response)
}

// streamContainerLogs suit le journal en SSE : un événement « line » par
// ligne, « end » quand le journal ou la machine s'arrête. C'est la seule
// seconde connexion qu'un onglet ouvre, plafonnée à part.
func (s *Server) streamContainerLogs(w http.ResponseWriter, r *http.Request) {
	item, ok := s.containerOr404(w, r)
	if !ok {
		return
	}
	if !s.takeLogSlot() {
		s.apiRefuse(w, http.StatusServiceUnavailable, codeLogsBusy)
		return
	}
	defer s.releaseLogSlot()
	batches, err := s.containers.FollowLogs(r.Context(), item.ID, tailOf(r))
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
			if s.authorizationLapsed(r) {
				return
			}
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

// takeLogSlot réserve une des seize lectures de journaux ; faux quand
// elles sont toutes prises. La comparaison et l'incrément se font d'un
// seul geste, sans quoi deux demandes passeraient sur la dernière place.
func (s *Server) takeLogSlot() bool {
	for {
		taken := s.logStreams.Load()
		if taken >= maxLogStreams {
			return false
		}
		if s.logStreams.CompareAndSwap(taken, taken+1) {
			return true
		}
	}
}

func (s *Server) releaseLogSlot() {
	s.logStreams.Add(-1)
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
	code, status, refused := logsRefusal(err)
	if !refused {
		s.apiInternalError(w, r, err)
		return
	}
	s.apiRefuse(w, status, code)
}

// logsRefusal classe un échec de lecture des journaux : la clé que le
// front ou le modèle lit, et le statut HTTP qui va avec.
func logsRefusal(err error) (string, int, bool) {
	var failed *container.LogsError
	switch {
	case errors.Is(err, container.ErrMachineOffline):
		return "container.machine_offline", http.StatusServiceUnavailable, true
	case errors.Is(err, container.ErrLogsUnavailableLocally):
		return "container.logs_unavailable_locally", http.StatusServiceUnavailable, true
	case errors.Is(err, machine.ErrBusy):
		return codeMachineBusy, http.StatusServiceUnavailable, true
	case errors.Is(err, container.ErrLogsBusy):
		return codeLogsBusy, http.StatusTooManyRequests, true
	case errors.Is(err, container.ErrLogsTimeout):
		return "container.logs_timeout", http.StatusGatewayTimeout, true
	case errors.As(err, &failed):
		return failed.Code, http.StatusBadGateway, true
	}
	return "", 0, false
}

func (s *Server) containerOr404(w http.ResponseWriter, r *http.Request) (container.Container, bool) {
	item, err := s.containers.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, container.ErrNotFound) {
		s.apiNotFound(w)
		return container.Container{}, false
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return container.Container{}, false
	}
	return item, true
}

func (s *Server) machineNames(ctx context.Context) (map[string]string, error) {
	statuses, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(statuses))
	for _, machineStatus := range statuses {
		names[machineStatus.ID] = machineStatus.Name
	}
	return names, nil
}

func (s *Server) currentByContainer(ctx context.Context) (map[string]container.Current, error) {
	currents, err := s.containers.CurrentAll(ctx)
	if err != nil {
		return nil, err
	}
	byContainer := make(map[string]container.Current, len(currents))
	for _, current := range currents {
		byContainer[current.ContainerID] = current
	}
	return byContainer, nil
}

func tailOf(r *http.Request) int {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	return tail
}

func containerToJSON(item container.Container, machineName string, current container.Current, checks map[string]update.Check) containerJSON {
	converted := containerJSON{
		ID: item.ID, MachineID: item.MachineID, MachineName: machineName, Kind: string(item.Kind),
		Name: item.Name, Group: item.Group, ContainerID: item.ContainerID, Image: item.Image, ImageID: item.ImageID,
		ComposeService: item.ComposeService, ComposeDir: item.ComposeDir, ComposeFile: item.ComposeFile,
		UpdatePolicy: string(item.UpdatePolicy), DeclaredServiceID: item.DeclaredServiceID, ImageCheck: imageCheckToJSON(item, checks),
		State: string(item.State), ExitCode: item.ExitCode, OOMKilled: item.OOMKilled, Killed: item.Killed, Health: string(item.Health), RestartCount: item.RestartCount,
		Ports: orEmpty(item.Ports), NetworkMode: item.NetworkMode, Privileged: item.Privileged,
		Networks: orEmpty(item.Networks), DependsOn: orEmpty(item.DependsOn), Exposure: container.Exposure(item),
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

func imageCheckToJSON(item container.Container, checks map[string]update.Check) *imageCheckJSON {
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

func engineToJSON(engine container.Engine) engineJSON {
	return engineJSON{
		MachineID: engine.MachineID, Present: engine.Present, Reason: string(engine.Reason),
		Version: engine.Version, APIVersion: engine.APIVersion, CheckedAt: engine.CheckedAt.UTC(),
	}
}

func transitionsToJSON(transitions []container.Transition) []transitionJSON {
	converted := make([]transitionJSON, 0, len(transitions))
	for _, t := range transitions {
		converted = append(converted, transitionJSON{
			ID: t.ID, At: t.At.UTC(), Action: t.Action, PreviousState: string(t.PreviousState), NewState: string(t.NewState),
			PreviousHealth: string(t.PreviousHealth), NewHealth: string(t.NewHealth), ExitCode: t.ExitCode, OOMKilled: t.OOMKilled, Killed: t.Killed, Replayed: t.Replayed, Snippet: t.Snippet,
		})
	}
	return converted
}

func logLineToJSON(line container.LogLine) logLineJSON {
	return logLineJSON{At: timeOrNil(line.At), Stream: line.Stream, Text: line.Text}
}
