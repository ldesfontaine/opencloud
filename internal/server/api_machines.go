package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
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
	// Le support du système, seulement quand l'opérateur a activé la fin
	// de support.
	Lifecycle *lifecycleJSON `json:"lifecycle,omitempty"`
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

func machineToJSON(machineStatus machine.Status) machineJSON {
	return machineJSON{
		ID:           machineStatus.ID,
		Name:         machineStatus.Name,
		Kind:         string(machineStatus.Kind),
		Hostname:     machineStatus.Hostname,
		Address:      machineStatus.Address,
		OS:           machineStatus.OS,
		Arch:         machineStatus.Arch,
		AgentVersion: machineStatus.AgentVersion,
		Local:        machineStatus.IsLocal(),
		Online:       machineStatus.Online,
		EnrolledAt:   machineStatus.EnrolledAt.UTC(),
		LastSeenAt:   httpx.TimeOrNil(machineStatus.LastSeenAt),
	}
}

// machineJSONOf ajoute aux faits de la machine le support de son système,
// quand la fin de support est activée.
func (s *Server) machineJSONOf(machineStatus machine.Status, withLifecycle bool) machineJSON {
	converted := machineToJSON(machineStatus)
	if withLifecycle {
		converted.Lifecycle = lifecycleToJSON(s.lifecycle.OfSystem(machineStatus.OS))
	}
	return converted
}

func (s *Server) listMachines(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.machines.List(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	pending, err := s.machines.PendingTokens(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	withLifecycle, err := s.lifecycleEnabled(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := machinesResponse{
		Machines: httpx.ConvertAll(statuses, func(machineStatus machine.Status) machineJSON { return s.machineJSONOf(machineStatus, withLifecycle) }),
		Pending:  make([]pendingTokenJSON, 0, len(pending)),
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
	found, err := s.machines.Get(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	withLifecycle, err := s.lifecycleEnabled(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, s.machineJSONOf(found, withLifecycle))
}

// L'opérateur nomme la machine ; le jeton naît et se montre une seule fois.
func (s *Server) createMachineToken(w http.ResponseWriter, r *http.Request) {
	var request tokenRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	cleartext, token, err := s.machines.CreateToken(r.Context(), request.Name)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeToken(w, r, token, cleartext)
}

func (s *Server) writeToken(w http.ResponseWriter, r *http.Request, token machine.Token, cleartext string) {
	url, isLocal := s.resolvePublicURL(r)
	language, err := s.language(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, tokenResponse{
		Name:      token.Name,
		Token:     cleartext,
		Command:   installCommand(url, cleartext, language),
		ExpiresAt: token.ExpiresAt.UTC(),
		URLLocal:  isLocal,
	})
}

// La commande à coller ; la langue n'y figure que si elle n'est pas celle
// par défaut, pour rester courte. Le jeton passe par printf, un builtin du
// shell : aucun processus ne le porte dans ses arguments, alors que
// /proc/<pid>/cmdline de l'agent et de sudo se lit depuis tous les comptes de
// la machine. Un jeton n'a que des minuscules, des chiffres et « _ » : les
// guillemets simples suffisent. Pas de « sudo VAR=… » : la variable finirait
// dans les arguments de sudo.
func installCommand(publicURL, token string, code lang.Code) string {
	command := "printf '%s\\n' '" + token + "' | sudo " + brand.Command + " agent -server " + publicURL
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
	if errors.Is(err, machine.ErrLocalMachine) {
		s.apiRefuse(w, http.StatusForbidden, "machine.local_kept")
		return
	}
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeToken(w, r, token, cleartext)
}
