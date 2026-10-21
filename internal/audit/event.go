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
	ObjectEnrollmentToken ObjectKind = "enrollment_token" // #nosec G101 -- un nom, pas un secret.
	ObjectProject         ObjectKind = "project"
	ObjectService         ObjectKind = "service"
	ObjectUnit            ObjectKind = "unit"
	ObjectIgnoredKey      ObjectKind = "ignored_key"
	ObjectProbe           ObjectKind = "probe"
	ObjectHeartbeat       ObjectKind = "heartbeat"
	ObjectStatusComponent ObjectKind = "status_component"
	ObjectIncident        ObjectKind = "incident"
	ObjectStatusPage      ObjectKind = "status_page"
	ObjectChannel         ObjectKind = "channel"
	ObjectMute            ObjectKind = "mute"
	ObjectAlert           ObjectKind = "alert"
	ObjectSettings        ObjectKind = "settings"
	ObjectAuth            ObjectKind = "auth"
	ObjectMCP             ObjectKind = "mcp"
	ObjectMCPToken        ObjectKind = "mcp_token" // #nosec G101 -- un nom, pas un secret.
	ObjectMCPSession      ObjectKind = "mcp_session"
	ObjectSecrets         ObjectKind = "secrets" // #nosec G101 -- un nom, pas un secret.
)

func ObjectKinds() []ObjectKind {
	return []ObjectKind{
		ObjectMachine, ObjectEnrollmentToken, ObjectProject, ObjectService, ObjectUnit, ObjectIgnoredKey,
		ObjectProbe, ObjectHeartbeat, ObjectStatusComponent, ObjectIncident, ObjectStatusPage,
		ObjectChannel, ObjectMute, ObjectAlert, ObjectSettings, ObjectAuth,
		ObjectMCP, ObjectMCPToken, ObjectMCPSession, ObjectSecrets,
	}
}

// Action est « sorte d'objet.verbe » : la sorte se lit dans l'action, le
// verbe se traduit seul.
type Action string

const (
	MachineEnroll   Action = "machine.enroll"
	MachineReenroll Action = "machine.reenroll"
	MachineDelete   Action = "machine.delete"

	EnrollmentTokenCreate Action = "enrollment_token.create" // #nosec G101 -- un nom, pas un secret.
	EnrollmentTokenCancel Action = "enrollment_token.cancel" // #nosec G101 -- un nom, pas un secret.

	ProjectCreate Action = "project.create"
	ProjectUpdate Action = "project.update"
	ProjectDelete Action = "project.delete"

	ServiceCreate Action = "service.create"
	ServiceUpdate Action = "service.update"
	ServiceDelete Action = "service.delete"
	ServiceReview Action = "service.review"

	UnitUpdatePolicy Action = "unit.update_policy"

	IgnoredKeyCreate Action = "ignored_key.create"
	IgnoredKeyDelete Action = "ignored_key.delete"

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

	StatusPageUpdate Action = "status_page.update"

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

	MCPEnable           Action = "mcp.enable"
	MCPDisable          Action = "mcp.disable"
	MCPRegenerateSecret Action = "mcp.regenerate_secret" // #nosec G101 -- un nom, pas un secret.
	MCPSetRedirectURIs  Action = "mcp.set_redirect_uris"

	MCPTokenCreate Action = "mcp_token.create" // #nosec G101 -- un nom, pas un secret.
	MCPTokenRevoke Action = "mcp_token.revoke" // #nosec G101 -- un nom, pas un secret.

	MCPSessionAuthorize Action = "mcp_session.authorize"
	MCPSessionRevoke    Action = "mcp_session.revoke"

	SecretsForget Action = "secrets.forget" // #nosec G101 -- un nom, pas un secret.
)

func Actions() []Action {
	return []Action{
		MachineEnroll, MachineReenroll, MachineDelete,
		EnrollmentTokenCreate, EnrollmentTokenCancel,
		ProjectCreate, ProjectUpdate, ProjectDelete,
		ServiceCreate, ServiceUpdate, ServiceDelete, ServiceReview,
		UnitUpdatePolicy,
		IgnoredKeyCreate, IgnoredKeyDelete,
		ProbeCreate, ProbeUpdate, ProbeDelete, ProbePause, ProbeResume,
		HeartbeatCreate, HeartbeatUpdate, HeartbeatDelete, HeartbeatPause, HeartbeatResume, HeartbeatRegenerateToken,
		StatusComponentCreate, StatusComponentUpdate, StatusComponentDelete,
		IncidentOpen, IncidentUpdate, IncidentPost, IncidentDelete,
		StatusPageUpdate,
		ChannelCreate, ChannelUpdate, ChannelDelete,
		MuteCreate, MuteDelete,
		AlertAcknowledge,
		SettingsUpdate,
		AuthEnable, AuthDisable, AuthChangePassword,
		MCPEnable, MCPDisable, MCPRegenerateSecret, MCPSetRedirectURIs,
		MCPTokenCreate, MCPTokenRevoke,
		MCPSessionAuthorize, MCPSessionRevoke,
		SecretsForget,
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

// Verbs rend chaque verbe une fois, dans l'ordre des actions.
func Verbs() []string {
	seen := map[string]bool{}
	verbs := []string{}
	for _, action := range Actions() {
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
}

// isForbiddenDetail dit si un nom de détail porte un secret. Un nom qui finit
// par l'un d'eux (« webhook_url », « client_secret ») l'est aussi ;
// « token_id » ou « password_changed » ne le sont pas.
func isForbiddenDetail(name string) bool {
	for _, forbidden := range []string{"password", "token", "secret", "url"} {
		if name == forbidden || strings.HasSuffix(name, "_"+forbidden) {
			return true
		}
	}
	return false
}
