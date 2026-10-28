package server

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/version"
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
	// Ce que la machine porte, ou est, pour Proxmox : voir api_proxmox.go.
	Proxmox proxmoxSummaryJSON `json:"proxmox"`
	// Ce que l'agent reconnaît sur la machine, par ses signes locaux ; vide
	// sans agent, ou quand rien n'est reconnu. Un rôle ne donne aucun droit.
	Roles []roleJSON `json:"roles"`
	// Le support du système, seulement quand l'opérateur a activé la fin
	// de support.
	Lifecycle *lifecycleJSON `json:"lifecycle,omitempty"`
}

type roleJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// L'agent d'une machine : ce qu'il rapporte de son fichier, ce que son
// jeton demandait, et s'ils diffèrent. L'agent applique ceux de son fichier.
type agentJSON struct {
	ID           string                   `json:"id"`
	Version      string                   `json:"version"`
	EnrolledAt   time.Time                `json:"enrolled_at"`
	RevokedAt    *time.Time               `json:"revoked_at"`
	Capabilities []reportedCapabilityJSON `json:"capabilities"`
	ReportedAt   *time.Time               `json:"reported_at"`
	Requested    []capabilityJSON         `json:"requested"`
	// Le fichier de l'agent ne dit pas ce que le jeton demandait.
	CapabilitiesDiffer bool `json:"capabilities_differ"`
	// L'empreinte de la clé du serveur que l'agent dit avoir épinglée ; vide
	// tant qu'il n'a rien dit. Différente de GET /api/agents/server-key :
	// l'agent refuserait les ordres de ce serveur.
	ServerKeyFingerprint string `json:"server_key_fingerprint"`
	// Le compte sous lequel l'agent dit tourner ; null tant qu'il ne l'a pas
	// dit (un agent d'avant).
	RunAs *accountJSON `json:"run_as"`
}

type capabilityJSON struct {
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

// Un droit tel que l'agent le rapporte : unavailable dit pourquoi son compte
// ne peut pas l'exercer sur la machine, vide s'il le peut. Le droit demandé
// par un jeton n'a pas ce champ.
type reportedCapabilityJSON struct {
	Name        string   `json:"name"`
	Paths       []string `json:"paths"`
	Unavailable string   `json:"unavailable"`
}

type accountJSON struct {
	UID  int64  `json:"uid"`
	Name string `json:"name"`
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
	Name    string `json:"name"`
	Token   string `json:"token"`
	Command string `json:"command"`
	// Les commandes à jouer une fois en root avant Command : le compte de
	// l'agent, son dossier, son binaire, les dossiers où ses droits écrivent.
	Preparation string    `json:"preparation"`
	ExpiresAt   time.Time `json:"expires_at"`
	URLLocal    bool      `json:"url_local"`
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
		Capabilities:       reportedCapabilitiesToJSON(agent),
		ReportedAt:         httpx.TimeOrNil(agent.ReportedAt),
		Requested:          httpx.ConvertAll(agent.Requested, capabilityToJSON),
		CapabilitiesDiffer: agent.CapabilitiesDiffer(),

		ServerKeyFingerprint: agent.ServerKeyFingerprint,
		RunAs:                accountToJSON(agent.RunAs),
	}
}

func accountToJSON(account *machine.Account) *accountJSON {
	if account == nil {
		return nil
	}
	return &accountJSON{UID: account.UID, Name: account.Name}
}

func reportedCapabilitiesToJSON(agent machine.Agent) []reportedCapabilityJSON {
	reported := make([]reportedCapabilityJSON, 0, len(agent.Capabilities))
	for _, capability := range agent.Capabilities {
		reported = append(reported, reportedCapabilityJSON{
			Name: capability.Name, Paths: orEmpty(capability.Paths), Unavailable: agent.UnavailableReason(capability.Name),
		})
	}
	return reported
}

func capabilityToJSON(capability machine.Capability) capabilityJSON {
	return capabilityJSON{Name: capability.Name, Paths: orEmpty(capability.Paths)}
}

func capabilityFromJSON(capability capabilityJSON) machine.Capability {
	return machine.Capability{Name: capability.Name, Paths: capability.Paths}
}

func roleToJSON(detected role.Role) roleJSON {
	return roleJSON{Name: string(detected.Name), Version: detected.Version}
}

// machineJSONOf ajoute aux faits de la machine ce qu'elle est pour Proxmox, ses
// rôles reconnus et le support de son système, quand la fin de support est
// activée.
func (s *Server) machineJSONOf(machineStatus machine.Status, summary proxmox.Summary, roles []role.Role, withLifecycle bool) machineJSON {
	converted := machineToJSON(machineStatus)
	converted.Proxmox = proxmoxSummaryToJSON(summary)
	converted.Roles = httpx.ConvertAll(roles, roleToJSON)
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
	// Les jetons d'enrôlement en attente sont des autorités : un jeton d'API
	// n'en voit aucun.
	var pending []machine.Token
	if _, byToken := apiTokenFrom(r.Context()); !byToken {
		pending, err = s.machines.PendingTokens(r.Context())
		if s.failed(w, r, err, nil) {
			return
		}
	}
	withLifecycle, err := s.lifecycleEnabled(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	summaries, err := s.reader.ProxmoxSummaries(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	roles, err := s.reader.MachineRoles(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := machinesResponse{
		Machines: httpx.ConvertAll(statuses, func(machineStatus machine.Status) machineJSON {
			return s.machineJSONOf(machineStatus, summaries[machineStatus.ID], roles[machineStatus.ID], withLifecycle)
		}),
		Pending: make([]pendingTokenJSON, 0, len(pending)),
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
	summaries, err := s.reader.ProxmoxSummaries(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	roles, err := s.reader.MachineRoles(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, s.machineJSONOf(found, summaries[found.ID], roles[found.ID], withLifecycle))
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
	s.writeToken(w, r, token, cleartext, false)
}

// local : le jeton est celui de la machine openCloud, dont le binaire est celui
// que le serveur a installé, pas celui qu'on a copié dans /tmp.
func (s *Server) writeToken(w http.ResponseWriter, r *http.Request, token machine.Token, cleartext string, local bool) {
	url, isLocal := s.resolvePublicURL(r)
	language, err := s.language(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, tokenResponse{
		Name:        token.Name,
		Token:       cleartext,
		Command:     installCommand(url, cleartext, language, token.Capabilities),
		Preparation: installPreparation(token.Capabilities, version.Number(), local),
		ExpiresAt:   token.ExpiresAt.UTC(),
		URLLocal:    isLocal,
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
// Le binaire est celui de l'état de l'agent, pas /usr/local/bin/opencloud : un
// lien root vers un fichier que le compte de l'agent peut remplacer serait une
// escalade sur une machine sans Docker.
func installCommand(publicURL, token string, code lang.Code, capabilities []machine.Capability) string {
	command := "printf '%s\\n' '" + token + "' | sudo -u " + brand.AgentAccount + " " + brand.AgentCurrentPath +
		" agent -server " + publicURL
	if code != lang.Default {
		command += " -lang " + string(code)
	}
	if rights := machine.FormatCapabilities(capabilities); rights != "" && rights != machine.CapabilityObserve {
		command += " -rights " + rights
	}
	return command
}

// Les commandes à jouer une fois, en root, avant la ligne d'installation : le
// compte de l'agent (dans le groupe docker si la machine en a un), son dossier
// d'état et son binaire, puis un dossier par droit qui écrit ailleurs, à lui.
// Le groupe docker équivaut à root : la page et la documentation le disent.
// Rien n'y est protégé pour le shell : le compte, les chemins de
// l'état et la version sont des constantes, et un chemin de droit ne porte
// que des lettres, chiffres et « . _ - / » (machine.ValidCapabilityPath).
// Jamais le jeton. useradd échoue sur un compte qui existe : sans effet sur
// les lignes suivantes, qui ne dépendent pas de lui. Le lien current ne bouge
// que si le binaire a été posé, pour ne jamais viser un fichier absent.
//
// La ligne se rejoue sur une machine déjà préparée (Changer les droits), où le
// compte possède son état et peut-être un agent compromis : root n'y agit
// jamais sur un nom que le compte contrôle. install -d suit un lien et donne
// sa cible ; bin/, le binaire et un dossier de droit sous un dossier du
// compte se font donc sous le compte (runuser), et un dossier de droit que
// root donne est d'abord vérifié n'être pas un lien.
func installPreparation(capabilities []machine.Capability, number string, local bool) string {
	account := brand.AgentAccount
	owner := "-o " + account + " -g " + account
	asAccount := "runuser -u " + account + " -- "
	source := "/tmp/" + brand.Command
	if local {
		source = "/usr/local/bin/" + brand.Command
	}
	binDir := brand.AgentStateDir + "/" + brand.AgentBinDir
	binaryName := brand.AgentBinaryPrefix + number
	lines := []string{
		"useradd --system --home-dir " + brand.AgentStateDir + " --shell /usr/sbin/nologin " + account,
		"getent group docker >/dev/null && usermod -aG docker " + account,
		"install -d -m 0700 " + owner + " " + brand.AgentStateDir,
		asAccount + "install -d -m 0700 " + binDir,
		asAccount + "install -m 0755 " + source + " " + binDir + "/" + binaryName +
			" && " + asAccount + "ln -sfn " + binaryName + " " + binDir + "/" + brand.AgentCurrentLink,
	}
	lines = append(lines, producePreparation(capabilities, asAccount)...)
	folders := writtenFolders(capabilities)
	accountFolders := []string{brand.AgentStateDir}
	for _, folder := range folders {
		accountFolders = append(accountFolders, folder.path)
	}
	// Un dossier sous un autre dossier du compte vient après lui, sous le compte.
	var nested []string
	for _, folder := range folders {
		comment := " # " + strings.Join(folder.rights, ", ")
		if insideAny(folder.path, accountFolders) {
			nested = append(nested, asAccount+"install -d -m "+folder.mode+" "+folder.path+comment)
			continue
		}
		lines = append(lines, "test ! -L "+folder.path+" && install -d -m "+folder.mode+" "+owner+" "+folder.path+comment)
	}
	return strings.Join(append(lines, nested...), "\n")
}

// producePreparation : ce que backup.produce=proxmox-vzdump demande de plus,
// une ligne par geste (six), après le compte. NoNewPrivileges=true, dans
// l'unité de l'agent, interdit tout sudo : le drop-in le desserre, c'est le
// seul desserrement. Le script de root et la règle sudo sont de root, sur des
// chemins que le compte ne contrôle pas ; seul le dossier de production, sous
// l'état, est fait sous le compte. Les textes viennent de machine/produce.go :
// aucune apostrophe, aucun nom de l'opérateur. Ce droit vaut root pour un agent
// compromis (vzdump --script), comme le groupe docker.
func producePreparation(capabilities []machine.Capability, asAccount string) []string {
	if !machine.CoversProducer(capabilities, machine.ProducerProxmoxVzdump) {
		return nil
	}
	return []string{
		asAccount + "install -d -m 0700 " + machine.ProduceDir,
		printfLines(machine.VzdumpWrapperLines()) + " | install -m 0755 -o root -g root /dev/stdin " + brand.VzdumpWrapperPath,
		sudoersPreparation(),
		"install -d -m 0755 " + machine.ProduceDropInDir,
		printfLines(machine.ProduceDropInLines()) + " | install -m 0644 /dev/stdin " + machine.ProduceDropInFile,
		"systemctl daemon-reload",
	}
}

// sudoersPreparation pose la règle sudo en un geste : écrite dans un fichier que
// sudo ignore, jugée par visudo, puis renommée. Si l'un des trois échoue, le
// fichier provisoire part et rien n'est posé ; la ligne rend alors une erreur
// (false), que le shell de l'opérateur voit.
func sudoersPreparation() string {
	temp := machine.SudoersTempFile
	return printfLines(machine.SudoersRuleLines()) + " | install -m 0440 -o root -g root /dev/stdin " + temp +
		" && visudo -cf " + temp + " && mv " + temp + " " + machine.SudoersFile +
		" || { rm -f " + temp + "; false; }"
}

// printfLines écrit des lignes de texte en une commande : printf, un builtin,
// une ligne par argument entre apostrophes. Les textes n'en portent pas.
func printfLines(lines []string) string {
	return "printf '%s\\n' '" + strings.Join(lines, "' '") + "'"
}

// insideAny dit si path est sous l'un des dossiers (pas l'un d'eux).
func insideAny(path string, folders []string) bool {
	return slices.ContainsFunc(folders, func(folder string) bool {
		return strings.HasPrefix(path, folder+"/")
	})
}

// Un dossier où un droit coché écrit, avec le mode que l'agent lui donne et
// les droits qui l'utilisent.
type writtenFolder struct {
	path   string
	mode   string
	rights []string
}

// writtenFolders liste les dossiers à préparer, chacun une fois, dans l'ordre
// des droits : la racine des services pour docker.deploy et docker.update, le
// dossier de backup.receive (0700 : ce sont des sauvegardes), ceux de
// files.write. Un dossier demandé par deux droits garde le mode du premier.
func writtenFolders(capabilities []machine.Capability) []writtenFolder {
	var folders []writtenFolder
	add := func(path, mode, right string) {
		// Les droits viennent normalisés ; un chemin qui ne l'est pas ne
		// s'écrit pas dans des lignes qu'on colle en root.
		if !machine.ValidCapabilityPath(path) {
			return
		}
		for index := range folders {
			if folders[index].path == path {
				if !slices.Contains(folders[index].rights, right) {
					folders[index].rights = append(folders[index].rights, right)
				}
				return
			}
		}
		folders = append(folders, writtenFolder{path: path, mode: mode, rights: []string{right}})
	}
	for _, capability := range capabilities {
		switch capability.Name {
		case machine.CapabilityDockerDeploy, machine.CapabilityDockerUpdate:
			add(brand.WorkspaceRoot, "0755", capability.Name)
		case machine.CapabilityBackupReceive:
			for _, path := range capability.Paths {
				add(path, "0700", capability.Name)
			}
		case machine.CapabilityFilesWrite:
			for _, path := range capability.Paths {
				add(path, "0755", capability.Name)
			}
		}
	}
	return folders
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
	s.writeToken(w, r, token, cleartext, token.MachineID == machine.LocalID)
}

// La ligne d'installation de l'agent de la machine openCloud, tant qu'elle
// n'en a jamais eu : l'écran de première configuration la montre, une fois
// le compte créé. 409 machine.local_enrolled ensuite.
func (s *Server) offerLocalEnrollment(w http.ResponseWriter, r *http.Request) {
	cleartext, token, err := s.machines.OfferLocalEnrollment(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeToken(w, r, token, cleartext, true)
}
