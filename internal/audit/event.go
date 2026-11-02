package audit

import (
	"strings"
	"time"
)

// Event est une ligne du journal : qui, quoi, quand, sur quel objet. L'objet
// n'a pas de clé étrangère : son nom est recopié, la ligne lui survit.
type Event struct {
	ID         int64
	At         time.Time
	Actor      Actor
	Action     Action
	ObjectKind ObjectKind
	ObjectID   string
	ObjectName string
	Details    Details
	// ApplyID est le passage d'apply qui a écrit l'objet, vide sinon.
	ApplyID string
}

// Details porte les champs utiles du geste, en texte et à plat : jamais un
// mot de passe, un jeton, un secret ni l'URL d'un webhook (voir
// isForbiddenDetail).
type Details map[string]string

// ObjectKind est la sorte d'objet visé.
type ObjectKind string

const (
	ObjectMachine         ObjectKind = "machine"
	ObjectAgent           ObjectKind = "agent"
	ObjectEnrollmentToken ObjectKind = "enrollment_token" // #nosec G101 -- un nom, pas un secret.
	ObjectProject         ObjectKind = "project"
	ObjectService         ObjectKind = "service"
	ObjectUnit            ObjectKind = "unit"
	ObjectIgnoredKey      ObjectKind = "ignored_key"
	// Un lien entre deux instances, écrit par l'opérateur.
	ObjectLink            ObjectKind = "link"
	ObjectProbe           ObjectKind = "probe"
	ObjectHeartbeat       ObjectKind = "heartbeat"
	ObjectStatusComponent ObjectKind = "status_component"
	ObjectIncident        ObjectKind = "incident"
	ObjectChannel         ObjectKind = "channel"
	ObjectMute            ObjectKind = "mute"
	ObjectAlert           ObjectKind = "alert"
	ObjectSettings        ObjectKind = "settings"
	ObjectAuth            ObjectKind = "auth"
	ObjectAPIToken        ObjectKind = "api_token" // #nosec G101 -- un nom, pas un secret.
	ObjectSecrets         ObjectKind = "secrets"   // #nosec G101 -- un nom, pas un secret.
	// Un ordre envoyé à un agent : l'objet est son run, nommé par sa machine.
	ObjectAction ObjectKind = "action"
	// La sauvegarde de la base : sa paire de clés, ses destinations, son plan.
	ObjectBackupKeyring     ObjectKind = "backup_keyring"
	ObjectBackupDestination ObjectKind = "backup_destination"
	ObjectBackupPlan        ObjectKind = "backup_plan"
	// Un pare-feu déclaré, lu en lecture seule.
	ObjectFirewall ObjectKind = "firewall"
	// Une cible restic (une machine qui lance rest-server en ajout seul), et
	// une sauvegarde restic d'une source vers elle.
	ObjectResticTarget ObjectKind = "restic_target"
	ObjectResticBackup ObjectKind = "restic_backup"
	// La demande qu'un jeton dépose pour un verbe à confirmation.
	ObjectVerb ObjectKind = "verb"
)

func ObjectKinds() []ObjectKind {
	return []ObjectKind{
		ObjectMachine, ObjectAgent, ObjectEnrollmentToken, ObjectProject, ObjectService, ObjectUnit, ObjectIgnoredKey, ObjectLink,
		ObjectProbe, ObjectHeartbeat, ObjectStatusComponent, ObjectIncident,
		ObjectChannel, ObjectMute, ObjectAlert, ObjectSettings, ObjectAuth,
		ObjectAPIToken, ObjectSecrets, ObjectAction,
		ObjectBackupKeyring, ObjectBackupDestination, ObjectBackupPlan,
		ObjectFirewall, ObjectVerb, ObjectResticTarget, ObjectResticBackup,
	}
}

// LegacyObjectKinds sont les sortes d'objet que le MCP écrivait jusqu'au
// 21 octobre 2026, et la page de statut, devenue la publication d'un projet :
// lues, plus jamais écrites. Le journal est en ajout seul,
// on ne réécrit pas l'histoire, on la lit.
func LegacyObjectKinds() []ObjectKind {
	return []ObjectKind{"mcp", "mcp_token", "mcp_session", "status_page"}
}

// Action est « sorte d'objet.verbe » : la sorte se lit dans l'action, le
// verbe se traduit seul.
type Action string

const (
	MachineReenroll Action = "machine.reenroll"
	MachineDelete   Action = "machine.delete"
	// L'hôte Proxmox déclaré sur une machine, ou retiré d'elle. Le secret du
	// jeton n'y figure pas : seulement son identifiant.
	MachineProxmoxSet   Action = "machine.proxmox_set"
	MachineProxmoxUnset Action = "machine.proxmox_unset"
	// Un geste demandé sur un invité de l'hôte (démarrer, arrêter, instantané) :
	// le numéro, le geste et le run. Aucun secret : le jeton ne quitte pas l'hôte.
	MachineProxmoxGuest Action = "machine.proxmox_guest"
	// Le drapeau « à mettre à jour en dernier », posé ou retiré par l'opérateur.
	MachineUpdateLast Action = "machine.update_last"
	// Le rôle de la machine, changé par l'opérateur.
	MachineRole Action = "machine.role"
	// Le type et l'hôte que l'opérateur a corrigés pour la machine.
	MachinePlacement Action = "machine.placement"

	// L'agent est enrôlé (la machine créée ou retrouvée) ou révoqué : l'objet
	// est l'agent, nommé par sa machine. Avant la table agents, l'enrôlement
	// s'écrivait machine.enroll.
	AgentEnroll Action = "agent.enroll"
	AgentRevoke Action = "agent.revoke"

	EnrollmentTokenCreate Action = "enrollment_token.create" // #nosec G101 -- un nom, pas un secret.
	EnrollmentTokenCancel Action = "enrollment_token.cancel" // #nosec G101 -- un nom, pas un secret.

	ProjectCreate Action = "project.create"
	ProjectUpdate Action = "project.update"
	ProjectDelete Action = "project.delete"

	ServiceCreate Action = "service.create"
	ServiceUpdate Action = "service.update"
	ServiceDelete Action = "service.delete"

	UnitUpdatePolicy Action = "unit.update_policy"

	IgnoredKeyCreate Action = "ignored_key.create"
	IgnoredKeyDelete Action = "ignored_key.delete"

	LinkCreate Action = "link.create"
	LinkDelete Action = "link.delete"
	// Les trois gestes sur un lien déduit : confirmé (il devient déclaré),
	// écarté, rétabli.
	LinkConfirm Action = "link.confirm"
	LinkDismiss Action = "link.dismiss"
	LinkRestore Action = "link.restore"

	ProbeCreate Action = "probe.create"
	ProbeUpdate Action = "probe.update"
	ProbeDelete Action = "probe.delete"
	ProbePause  Action = "probe.pause"
	ProbeResume Action = "probe.resume"

	HeartbeatCreate          Action = "heartbeat.create"
	HeartbeatUpdate          Action = "heartbeat.update"
	HeartbeatDelete          Action = "heartbeat.delete"
	HeartbeatPause           Action = "heartbeat.pause"
	HeartbeatResume          Action = "heartbeat.resume"
	HeartbeatRegenerateToken Action = "heartbeat.regenerate_token" // #nosec G101 -- un nom, pas un secret.

	StatusComponentCreate Action = "status_component.create"
	StatusComponentUpdate Action = "status_component.update"
	StatusComponentDelete Action = "status_component.delete"

	IncidentOpen   Action = "incident.open"
	IncidentUpdate Action = "incident.update"
	IncidentPost   Action = "incident.post"
	IncidentDelete Action = "incident.delete"

	ChannelCreate Action = "channel.create"
	ChannelUpdate Action = "channel.update"
	ChannelDelete Action = "channel.delete"

	MuteCreate Action = "mute.create"
	MuteDelete Action = "mute.delete"

	AlertAcknowledge Action = "alert.acknowledge"

	SettingsUpdate Action = "settings.update"

	AuthEnable         Action = "auth.enable"
	AuthDisable        Action = "auth.disable"
	AuthChangePassword Action = "auth.change_password" // #nosec G101 -- un nom, pas un secret.

	APITokenCreate Action = "api_token.create" // #nosec G101 -- un nom, pas un secret.
	APITokenRevoke Action = "api_token.revoke" // #nosec G101 -- un nom, pas un secret.
	// La portée d'un jeton (ses machines, ses invités) remplacée.
	APITokenUpdate Action = "api_token.update" // #nosec G101 -- un nom, pas un secret.
	// Un jeton qui a tenté un verbe qu'il ne porte pas.
	APITokenDeny Action = "api_token.deny" // #nosec G101 -- un nom, pas un secret.

	SecretsForget Action = "secrets.forget" // #nosec G101 -- un nom, pas un secret.
	// La clé de chiffrement changée, chaque secret rescellé ; la clé qui
	// signe les ordres changée, puis la précédente retirée.
	SecretsRotate          Action = "secrets.rotate"            // #nosec G101 -- un nom, pas un secret.
	SecretsRotateServerKey Action = "secrets.rotate_server_key" // #nosec G101 -- un nom, pas un secret.
	SecretsFinishServerKey Action = "secrets.finish_server_key" // #nosec G101 -- un nom, pas un secret.

	// Un ordre écrit et signé, avant de partir vers l'agent ; refusé par
	// l'agent, avec sa raison.
	ActionOrder   Action = "action.order"
	ActionRefused Action = "action.refused"
	// L'agent est revenu à sa version d'avant, seul, parce que la nouvelle
	// n'a pas confirmé : l'acteur est l'agent de la machine. Aussi quand la
	// remise en état a échoué à son tour.
	ActionRolledBack Action = "action.rolled_back"

	// La phrase de passe n'est jamais écrite ici : seulement l'empreinte de
	// la clé qu'elle scelle.
	BackupKeyringCreate     Action = "backup_keyring.create"
	BackupKeyringReplace    Action = "backup_keyring.replace"
	BackupDestinationCreate Action = "backup_destination.create"
	BackupDestinationDelete Action = "backup_destination.delete"
	BackupPlanUpdate        Action = "backup_plan.update"
	BackupPlanDelete        Action = "backup_plan.delete"
	// Une sauvegarde lancée, par l'opérateur ou par la règle de la nuit.
	BackupPlanRun Action = "backup_plan.run"

	// Une cible restic posée ou retirée : l'adresse, le port et le dossier,
	// jamais un mot de passe. Une sauvegarde restic déclarée, changée, retirée
	// ou lancée (par l'opérateur ou la règle de la nuit) : la source, la cible
	// et ce qui part, jamais l'empreinte du mot de passe REST.
	ResticTargetSetup  Action = "restic_target.setup"
	ResticTargetRemove Action = "restic_target.remove"
	ResticBackupCreate Action = "restic_backup.create"
	ResticBackupUpdate Action = "restic_backup.update"
	ResticBackupDelete Action = "restic_backup.delete"
	ResticBackupRun    Action = "restic_backup.run"
	// Le dépôt d'une sauvegarde vérifié par sa cible (restic.verify), à la
	// main ou par la règle de la nuit : jamais le mot de passe du dépôt.
	ResticBackupVerify Action = "restic_backup.verify"
	// Les sauvegardes parcourues dans l'explorateur : l'instantané et le
	// dossier lus, jamais un nom de fichier ni le mot de passe du dépôt.
	ResticBackupBrowse Action = "restic_backup.browse"

	// Un pare-feu déclaré, redéclaré ou retiré. Ni la clé ni le secret n'y
	// figurent : seulement l'hôte de l'API et si l'empreinte est épinglée.
	FirewallCreate Action = "firewall.create"
	FirewallUpdate Action = "firewall.update"
	FirewallDelete Action = "firewall.delete"

	// La demande d'un verbe à confirmation : déposée par un jeton, refusée par
	// l'opérateur, ou échue sans réponse. La confirmation n'a pas de ligne à
	// elle : le geste confirmé s'écrit sous l'opérateur, avec request_id.
	VerbRequest Action = "verb.request"
	VerbReject  Action = "verb.reject"
	VerbExpire  Action = "verb.expire"
)

func Actions() []Action {
	return []Action{
		MachineReenroll, MachineDelete, MachineProxmoxSet, MachineProxmoxUnset, MachineProxmoxGuest, MachineUpdateLast, MachineRole, MachinePlacement,
		AgentEnroll, AgentRevoke,
		EnrollmentTokenCreate, EnrollmentTokenCancel,
		ProjectCreate, ProjectUpdate, ProjectDelete,
		ServiceCreate, ServiceUpdate, ServiceDelete,
		UnitUpdatePolicy,
		IgnoredKeyCreate, IgnoredKeyDelete,
		LinkCreate, LinkDelete, LinkConfirm, LinkDismiss, LinkRestore,
		ProbeCreate, ProbeUpdate, ProbeDelete, ProbePause, ProbeResume,
		HeartbeatCreate, HeartbeatUpdate, HeartbeatDelete, HeartbeatPause, HeartbeatResume, HeartbeatRegenerateToken,
		StatusComponentCreate, StatusComponentUpdate, StatusComponentDelete,
		IncidentOpen, IncidentUpdate, IncidentPost, IncidentDelete,
		ChannelCreate, ChannelUpdate, ChannelDelete,
		MuteCreate, MuteDelete,
		AlertAcknowledge,
		SettingsUpdate,
		AuthEnable, AuthDisable, AuthChangePassword,
		APITokenCreate, APITokenUpdate, APITokenRevoke, APITokenDeny,
		SecretsForget, SecretsRotate, SecretsRotateServerKey, SecretsFinishServerKey,
		ActionOrder, ActionRefused, ActionRolledBack,
		BackupKeyringCreate, BackupKeyringReplace, BackupDestinationCreate, BackupDestinationDelete,
		BackupPlanUpdate, BackupPlanDelete, BackupPlanRun,
		FirewallCreate, FirewallUpdate, FirewallDelete,
		VerbRequest, VerbReject, VerbExpire,
		ResticTargetSetup, ResticTargetRemove, ResticBackupCreate, ResticBackupUpdate, ResticBackupDelete, ResticBackupRun, ResticBackupVerify, ResticBackupBrowse,
	}
}

// LegacyActions sont les actions que le MCP écrivait jusqu'au 21 octobre
// 2026, et celles des pages de statut avant qu'elles entrent dans le projet :
// lues, plus jamais écrites (voir LegacyObjectKinds).
func LegacyActions() []Action {
	return []Action{
		"mcp.enable", "mcp.disable", "mcp.regenerate_secret", "mcp.set_redirect_uris",
		"mcp_token.create", "mcp_token.revoke",
		"mcp_session.authorize", "mcp_session.deny", "mcp_session.revoke",
		"status_page.create", "status_page.update", "status_page.delete",
	}
}

// ObjectKind est la sorte d'objet que l'action vise.
func (a Action) ObjectKind() ObjectKind {
	kind, _, _ := strings.Cut(string(a), ".")
	return ObjectKind(kind)
}

// Verb est ce que l'action fait, sans l'objet : c'est ce que l'interface
// traduit.
func (a Action) Verb() string {
	_, verb, _ := strings.Cut(string(a), ".")
	return verb
}

// Verbs rend chaque verbe une fois, dans l'ordre des actions, les anciennes
// à la suite : une ancienne ligne se traduit encore.
func Verbs() []string {
	seen := map[string]bool{}
	verbs := []string{}
	for _, action := range append(Actions(), LegacyActions()...) {
		verb := action.Verb()
		if !seen[verb] {
			seen[verb] = true
			verbs = append(verbs, verb)
		}
	}
	return verbs
}

// Query choisit une page du journal, du plus récent au plus ancien. Before
// est l'identifiant sous lequel reprendre ; zéro part du plus récent.
type Query struct {
	Limit      int
	Before     int64
	ObjectKind ObjectKind
	ObjectID   string
	// ActorKind ne garde que ce genre d'acteur ; vide : tous.
	ActorKind ActorKind
	// Actor cherche un morceau de texte, sans casse ASCII, dans le nom du
	// jeton (actor_label), l'identifiant (actor_name) ou « pour qui »
	// (on_behalf_of) ; vide : tous.
	Actor string
}

// isForbiddenDetail dit si un nom de détail porte un secret. Un nom qui finit
// par l'un d'eux (« webhook_url », « client_secret ») l'est aussi ;
// « token_id » ou « password_changed » ne le sont pas.
func isForbiddenDetail(name string) bool {
	for _, forbidden := range []string{"password", "passphrase", "token", "secret", "url"} {
		if name == forbidden || strings.HasSuffix(name, "_"+forbidden) {
			return true
		}
	}
	return false
}
