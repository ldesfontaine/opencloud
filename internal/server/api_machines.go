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
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Hostname   string     `json:"hostname"`
	Address    string     `json:"address"`
	OS         string     `json:"os"`
	Arch       string     `json:"arch"`
	Online     bool       `json:"online"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	// L'agent courant, null tant qu'aucun ne s'est enrôlé.
	Agent *agentJSON `json:"agent"`
	// Le support du système, seulement quand l'opérateur a activé la fin
	// de support.
	Lifecycle *lifecycleJSON `json:"lifecycle,omitempty"`
}

// L'agent d'une machine : ce qu'il rapporte de son fichier, ce que son
// jeton demandait, et s'ils diffèrent. L'agent applique ceux de son fichier.
type agentJSON struct {
	ID           string           `json:"id"`
	Version      string           `json:"version"`
	EnrolledAt   time.Time        `json:"enrolled_at"`
	RevokedAt    *time.Time       `json:"revoked_at"`
	Capabilities []capabilityJSON `json:"capabilities"`
	ReportedAt   *time.Time       `json:"reported_at"`
	Requested    []capabilityJSON `json:"requested"`
	// Le fichier de l'agent ne dit pas ce que le jeton demandait.
	CapabilitiesDiffer bool `json:"capabilities_differ"`
	// L'empreinte de la clé du serveur que l'agent dit avoir épinglée ; vide
	// tant qu'il n'a rien dit. Différente de GET /api/agents/server-key :
	// l'agent refuserait les ordres de ce serveur.
	ServerKeyFingerprint string `json:"server_key_fingerprint"`
}

type capabilityJSON struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
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
	// Les droits cochés ; observe s'y ajoute toujours.
	Capabilities []capabilityJSON `json:"capabilities"`
}

// Ré-enrôler, avec les droits du nouvel agent ; absents, ceux du jeton de
// l'agent courant.
type reenrollRequest struct {
	Capabilities *[]capabilityJSON `json:"capabilities"`
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
		ID:         machineStatus.ID,
		Name:       machineStatus.Name,
		Hostname:   machineStatus.Hostname,
		Address:    machineStatus.Address,
		OS:         machineStatus.OS,
		Arch:       machineStatus.Arch,
		Online:     machineStatus.Online,
		LastSeenAt: httpx.TimeOrNil(machineStatus.LastSeenAt),
		Agent:      agentToJSON(machineStatus.Agent),
	}
}

func agentToJSON(agent machine.Agent) *agentJSON {
	if !agent.Exists() {
		return nil
	}
	return &agentJSON{
		ID:                 agent.ID,
		Version:            agent.Version,
		EnrolledAt:         agent.EnrolledAt.UTC(),
		RevokedAt:          httpx.TimeOrNil(agent.RevokedAt),
		Capabilities:       httpx.ConvertAll(agent.Capabilities, capabilityToJSON),
		ReportedAt:         httpx.TimeOrNil(agent.ReportedAt),
		Requested:          httpx.ConvertAll(agent.Requested, capabilityToJSON),
		CapabilitiesDiffer: agent.CapabilitiesDiffer(),

		ServerKeyFingerprint: agent.ServerKeyFingerprint,
	}
}

func capabilityToJSON(capability machine.Capability) capabilityJSON {
	return capabilityJSON{Name: capability.Name, Paths: orEmpty(capability.Paths)}
}

func capabilityFromJSON(capability capabilityJSON) machine.Capability {
	return machine.Capability{Name: capability.Name, Paths: capability.Paths}
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
	capabilities := httpx.ConvertAll(request.Capabilities, capabilityFromJSON)
	cleartext, token, err := s.machines.CreateToken(r.Context(), request.Name, capabilities)
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
		Command:   installCommand(url, cleartext, language, token.Capabilities),
		ExpiresAt: token.ExpiresAt.UTC(),
		URLLocal:  isLocal,
	})
}

// La commande à coller ; la langue n'y figure que si elle n'est pas celle
// par défaut, les droits que s'ils dépassent observe, pour rester courte. Le
// jeton passe par printf, un builtin du shell : aucun processus ne le porte
// dans ses arguments, alors que /proc/<pid>/cmdline de l'agent et de sudo se
// lit depuis tous les comptes de la machine. Un jeton n'a que des
// minuscules, des chiffres et « _ » : les guillemets simples suffisent. Pas
// de « sudo VAR=… » : la variable finirait dans les arguments de sudo. Les
// droits, eux, ne sont pas un secret : ils restent lisibles dans la ligne,
// et leur forme (lettres, chiffres, « . _ - / = : , ») passe sans guillemets.
func installCommand(publicURL, token string, code lang.Code, capabilities []machine.Capability) string {
	command := "printf '%s\\n' '" + token + "' | sudo " + brand.Command + " agent -server " + publicURL
	if code != lang.Default {
		command += " -lang " + string(code)
	}
	if rights := machine.FormatCapabilities(capabilities); rights != "" && rights != machine.CapabilityObserve {
		command += " -rights " + rights
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
	s.finishAPIAction(w, r, err, machine.ErrNotFound)
}

// Révoquer : l'agent est refusé, la machine et son historique restent.
func (s *Server) revokeAgent(w http.ResponseWriter, r *http.Request) {
	err := s.machines.Revoke(r.Context(), r.PathValue("id"))
	s.finishAPIAction(w, r, err, machine.ErrNotFound)
}

// Ré-enrôler : un nouveau jeton pour la même machine, qui garde son id.
// C'est aussi changer les droits de son agent : sans capabilities (ou sans
// corps), ceux d'avant.
func (s *Server) reenrollMachine(w http.ResponseWriter, r *http.Request) {
	var request reenrollRequest
	if r.ContentLength != 0 && !s.readAPI(w, r, &request) {
		return
	}
	var capabilities []machine.Capability
	if request.Capabilities != nil {
		capabilities = httpx.ConvertAll(*request.Capabilities, capabilityFromJSON)
	}
	cleartext, token, err := s.machines.CreateReenrollToken(r.Context(), r.PathValue("id"), capabilities)
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	s.writeToken(w, r, token, cleartext)
}

// La ligne d'installation de l'agent de la machine openCloud, tant qu'elle
// n'en a jamais eu : l'écran de première configuration la montre, une fois
// le compte créé. 409 machine.local_enrolled ensuite.
func (s *Server) offerLocalEnrollment(w http.ResponseWriter, r *http.Request) {
	cleartext, token, err := s.machines.OfferLocalEnrollment(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeToken(w, r, token, cleartext)
}
