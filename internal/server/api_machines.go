package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Une machine telle que le front la lit : des faits et des instants, pas
// de texte formaté ; « vu il y a » se calcule dans le navigateur.
type machineJSON struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	Hostname     string     `json:"hostname"`
	Address      string     `json:"address"`
	OS           string     `json:"os"`
	Arch         string     `json:"arch"`
	AgentVersion string     `json:"agent_version"`
	Local        bool       `json:"local"`
	Online       bool       `json:"online"`
	EnrolledAt   time.Time  `json:"enrolled_at"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
}

type pendingTokenJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Masked    string    `json:"masked"`
	ExpiresAt time.Time `json:"expires_at"`
	Reenroll  bool      `json:"reenroll"`
}

type machinesResponse struct {
	Machines []machineJSON      `json:"machines"`
	Pending  []pendingTokenJSON `json:"pending"`
}

type tokenRequest struct {
	Name string `json:"name"`
}

// Le jeton en clair, une seule fois, avec la commande à coller.
type tokenResponse struct {
	Name      string    `json:"name"`
	Token     string    `json:"token"`
	Command   string    `json:"command"`
	ExpiresAt time.Time `json:"expires_at"`
	URLLocal  bool      `json:"url_local"`
}

func machineToJSON(status machine.Status) machineJSON {
	return machineJSON{
		ID:           status.ID,
		Name:         status.Name,
		Kind:         string(status.Kind),
		Hostname:     status.Hostname,
		Address:      status.Address,
		OS:           status.OS,
		Arch:         status.Arch,
		AgentVersion: status.AgentVersion,
		Local:        status.IsLocal(),
		Online:       status.Online,
		EnrolledAt:   status.EnrolledAt.UTC(),
		LastSeenAt:   timeOrNil(status.LastSeenAt),
	}
}

// Un instant zéro devient null : le front n'a pas à connaître l'an 1. Tout
// part en UTC : l'API ne dépend pas du fuseau de la machine openCloud.
func timeOrNil(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}

func (s *Server) listMachines(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.machines.List(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	pending, err := s.machines.PendingTokens(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := machinesResponse{
		Machines: make([]machineJSON, 0, len(statuses)),
		Pending:  make([]pendingTokenJSON, 0, len(pending)),
	}
	for _, status := range statuses {
		response.Machines = append(response.Machines, machineToJSON(status))
	}
	for _, token := range pending {
		response.Pending = append(response.Pending, pendingTokenJSON{
			ID:        token.ID,
			Name:      token.Name,
			Masked:    token.Masked(),
			ExpiresAt: token.ExpiresAt.UTC(),
			Reenroll:  token.MachineID != "",
		})
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getMachine(w http.ResponseWriter, r *http.Request) {
	status, err := s.machines.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, machine.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, machineToJSON(status))
}

// L'opérateur nomme la machine ; le jeton naît et se montre une seule fois.
func (s *Server) createMachineToken(w http.ResponseWriter, r *http.Request) {
	var request tokenRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	cleartext, token, err := s.machines.CreateToken(r.Context(), request.Name)
	if errors.Is(err, machine.ErrNameInvalid) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, "machine.name_invalid")
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeToken(w, r, token, cleartext)
}

func (s *Server) writeToken(w http.ResponseWriter, r *http.Request, token machine.Token, cleartext string) {
	url, isLocal := s.resolvePublicURL(r)
	s.writeAPI(w, http.StatusCreated, tokenResponse{
		Name:      token.Name,
		Token:     cleartext,
		Command:   installCommand(url, cleartext, s.language()),
		ExpiresAt: token.ExpiresAt.UTC(),
		URLLocal:  isLocal,
	})
}

// La commande à coller ; la langue n'y figure que si elle n'est pas celle
// par défaut, pour rester courte.
func installCommand(publicURL, token string, code lang.Code) string {
	command := "sudo opencloud agent -server " + publicURL + " -token " + token
	if code != lang.Default {
		command += " -lang " + string(code)
	}
	return command
}

// Un jeton déjà parti est un jeton annulé : 204 dans les deux cas.
func (s *Server) cancelMachineToken(w http.ResponseWriter, r *http.Request) {
	err := s.machines.CancelToken(r.Context(), r.PathValue("id"))
	if err != nil && !errors.Is(err, machine.ErrTokenNotFound) {
		s.apiInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeMachine(w http.ResponseWriter, r *http.Request) {
	err := s.machines.Remove(r.Context(), r.PathValue("id"))
	if errors.Is(err, machine.ErrLocalMachine) {
		s.apiRefuse(w, http.StatusForbidden, "machine.local_kept")
		return
	}
	s.finishAPIAction(w, r, err, machine.ErrNotFound)
}

// Ré-enrôler : un nouveau jeton pour la même machine, qui garde son id.
func (s *Server) reenrollMachine(w http.ResponseWriter, r *http.Request) {
	cleartext, token, err := s.machines.CreateReenrollToken(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, machine.ErrLocalMachine):
		s.apiRefuse(w, http.StatusForbidden, "machine.local_kept")
	case errors.Is(err, machine.ErrNotFound):
		s.apiNotFound(w)
	case err != nil:
		s.apiInternalError(w, r, err)
	default:
		s.writeToken(w, r, token, cleartext)
	}
}
