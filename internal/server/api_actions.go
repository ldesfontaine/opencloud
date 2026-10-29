package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

const (
	// Les derniers ordres : ce que la fiche d'une machine montre d'un coup.
	actionsDefaultLimit = 20
	actionsMaxLimit     = 100
)

// Un ordre et ce qu'il est devenu, tel que le front le lit.
type actionRunJSON struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Les paramètres de l'ordre, sans secret ; {} pour un ordre qui n'en
	// prend pas.
	Params      json.RawMessage `json:"params"`
	MachineID   *string         `json:"machine_id"`
	MachineName string          `json:"machine_name"`
	AgentID     *string         `json:"agent_id"`
	// Le service déclaré qu'un ordre de déploiement vise ; null pour les
	// autres.
	ServiceID *string `json:"service_id"`
	State     string  `json:"state"`
	// Pourquoi l'ordre n'a pas abouti : la raison de l'agent, ou
	// « undelivered » ; vide sinon.
	Reason      string        `json:"reason"`
	Seq         int64         `json:"seq"`
	RequestedBy string        `json:"requested_by"`
	RequestedAt time.Time     `json:"requested_at"`
	NotAfter    time.Time     `json:"not_after"`
	FinishedAt  *time.Time    `json:"finished_at"`
	Output      []runLineJSON `json:"output"`
}

type runLineJSON struct {
	At     time.Time `json:"at"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

type actionsResponse struct {
	Runs []actionRunJSON `json:"runs"`
}

// La clé publique qui signe les ordres, et son empreinte : ce que chaque
// agent épingle à l'enrôlement et que l'opérateur compare.
type serverKeyJSON struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	// La clé précédente, le temps d'une rotation : un agent qui l'a épinglée
	// reçoit la neuve à l'ouverture de son flux. null hors rotation.
	PreviousFingerprint *string `json:"previous_fingerprint"`
}

func actionRunToJSON(run action.Run) actionRunJSON {
	params := json.RawMessage(run.Params)
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	return actionRunJSON{
		ID: run.ID, Kind: string(run.Kind), Params: params,
		MachineID: stringOrNil(run.MachineID), MachineName: run.MachineName, AgentID: stringOrNil(run.AgentID),
		ServiceID: stringOrNil(run.ServiceID),
		State:     string(run.State), Reason: run.Reason, Seq: run.Seq,
		RequestedBy: run.RequestedBy, RequestedAt: run.RequestedAt.UTC(), NotAfter: run.NotAfter.UTC(),
		FinishedAt: httpx.TimeOrNil(run.FinishedAt),
		Output:     httpx.ConvertAll(run.Output, runLineToJSON),
	}
}

func runLineToJSON(line action.LogLine) runLineJSON {
	return runLineJSON{At: line.At.UTC(), Stream: line.Stream, Line: line.Line}
}

func stringOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// pingAgent envoie l'ordre agent.ping à l'agent de la machine : le run est
// écrit et journalisé avant de partir, la réponse de l'agent le fait avancer.
func (s *Server) pingAgent(w http.ResponseWriter, r *http.Request) {
	run, err := s.actions.Ping(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}

// Ce que la fiche de la machine propose à son agent : la version du serveur,
// ou pourquoi pas. reason est vide quand la mise à jour est disponible, sauf
// right_unavailable : elle l'est quand même, le compte de l'agent ne peut pas
// exercer le droit et l'agent refusera avec la raison.
type agentUpdateOfferJSON struct {
	Current   string `json:"current"`
	Target    string `json:"target"`
	Arch      string `json:"arch"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// getAgentUpdate dit si l'agent de la machine peut recevoir la version du
// serveur, et sinon pourquoi.
func (s *Server) getAgentUpdate(w http.ResponseWriter, r *http.Request) {
	offer, err := s.actions.OfferUpdate(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, agentUpdateOfferJSON{
		Current: offer.Current, Target: offer.Target, Arch: offer.Arch, Available: offer.Available, Reason: offer.Reason,
	})
}

// updateAgent envoie l'ordre agent.update : le serveur revérifie la version
// importée, l'agent la revérifie à son tour. L'ordre est fini quand l'agent
// revient avec la nouvelle version.
func (s *Server) updateAgent(w http.ResponseWriter, r *http.Request) {
	run, err := s.actions.UpdateAgent(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}

// rollbackAgent envoie l'ordre agent.rollback : l'agent rebascule sur la
// version d'avant et redémarre ; elle est confirmée ou annulée comme une mise
// à jour.
func (s *Server) rollbackAgent(w http.ResponseWriter, r *http.Request) {
	run, err := s.actions.RollbackAgent(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}

// Le plan des mises à jour d'agent : une ligne par machine, et la mise à jour
// en vol s'il y en a une. order est le rang recommandé, null quand rien n'est
// à faire ou que rien ne peut partir. Le serveur n'exécute rien du plan.
type agentUpdatePlanJSON struct {
	Target     string                     `json:"target"`
	InProgress *agentUpdateInProgressJSON `json:"in_progress"`
	Machines   []agentUpdatePlanEntryJSON `json:"machines"`
}

type agentUpdateInProgressJSON struct {
	RunID       string    `json:"run_id"`
	Kind        string    `json:"kind"`
	MachineID   *string   `json:"machine_id"`
	MachineName string    `json:"machine_name"`
	RequestedAt time.Time `json:"requested_at"`
}

type agentUpdatePlanEntryJSON struct {
	MachineID       string `json:"machine_id"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Target          string `json:"target"`
	UpToDate        bool   `json:"up_to_date"`
	RollbackCapable bool   `json:"rollback_capable"`
	Online          bool   `json:"online"`
	UpdateLast      bool   `json:"update_last"`
	// Une raison de la fiche (machine.agent_update_reason_*) ; vide si l'ordre
	// peut partir, ou si la machine est à jour.
	BlockedReason string `json:"blocked_reason"`
	Order         *int   `json:"order"`
}

func planEntryToJSON(entry action.PlanEntry) agentUpdatePlanEntryJSON {
	var order *int
	if entry.Order > 0 {
		order = &entry.Order
	}
	return agentUpdatePlanEntryJSON{
		MachineID: entry.MachineID, Name: entry.MachineName, Version: entry.Version, Target: entry.Target,
		UpToDate: entry.UpToDate, RollbackCapable: entry.RollbackCapable, Online: entry.Online,
		UpdateLast: entry.UpdateLast, BlockedReason: entry.Blocked, Order: order,
	}
}

// listAgentUpdates lit le plan. Ouverte aux jetons en lecture : un agent IA
// y voit l'ordre recommandé, puis met à jour une machine à la fois.
func (s *Server) listAgentUpdates(w http.ResponseWriter, r *http.Request) {
	entries, err := s.actions.UpdatePlan(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	plan := agentUpdatePlanJSON{Target: s.actions.TargetVersion(), Machines: httpx.ConvertAll(entries, planEntryToJSON)}
	inFlight, found, err := s.actions.UpdateInFlight(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	if found {
		plan.InProgress = &agentUpdateInProgressJSON{
			RunID: inFlight.ID, Kind: string(inFlight.Kind), MachineID: stringOrNil(inFlight.MachineID),
			MachineName: inFlight.MachineName, RequestedAt: inFlight.RequestedAt.UTC(),
		}
	}
	s.writeAPI(w, http.StatusOK, plan)
}

// listActions rend les derniers ordres, d'une machine ou de toutes.
func (s *Server) listActions(w http.ResponseWriter, r *http.Request) {
	query, ok := actionQueryOf(r.URL.Query())
	if !ok {
		s.writeAPIError(w, http.StatusBadRequest, codeBadQuery)
		return
	}
	response, err := s.actionsPage(r.Context(), query)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

func actionQueryOf(values url.Values) (action.Query, bool) {
	query := action.Query{MachineID: values.Get("machine")}
	if text := values.Get("limit"); text != "" {
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 1 {
			return action.Query{}, false
		}
		query.Limit = limit
	}
	return query, true
}

// actionsPage borne la liste et l'assemble : l'API la rend telle quelle.
func (s *Server) actionsPage(ctx context.Context, query action.Query) (actionsResponse, error) {
	if query.Limit <= 0 {
		query.Limit = actionsDefaultLimit
	}
	query.Limit = min(query.Limit, actionsMaxLimit)
	runs, err := s.reader.ListActionRuns(ctx, query)
	if err != nil {
		return actionsResponse{}, err
	}
	return actionsResponse{Runs: httpx.ConvertAll(runs, actionRunToJSON)}, nil
}

// getServerKey rend la clé publique qui signe les ordres et son empreinte.
func (s *Server) getServerKey(w http.ResponseWriter, r *http.Request) {
	public, fingerprint, err := s.actions.ServerKey()
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, serverKeyJSON{
		PublicKey: base64.StdEncoding.EncodeToString(public), Fingerprint: fingerprint,
		PreviousFingerprint: stringOrNil(s.actions.PreviousServerKey()),
	})
}
