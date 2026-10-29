package server

import (
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/mcpcheck"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Une liste fermée du serveur, et le préfixe sous lequel le front ou le
// canal en traduit chaque valeur.
type composedKeys struct {
	prefix string
	suffix string
	values []string
}

// Le front et le canal composent des clés à l'exécution : probe.reason_ +
// motif, job.status_ + état. Le test des clés en dur ne les voit pas, et le
// test de lang ne compare que les langues entre elles : un motif ajouté en
// Go sans sa clé passerait tout et s'afficherait entre crochets, dans le
// navigateur comme dans le message envoyé au canal. Ici, chaque valeur de
// chaque liste doit avoir sa clé dans chaque langue.
func TestCatalog_HasAKeyForEveryValueOfEveryClosedList(t *testing.T) {
	server := newTestServer(t)
	for _, list := range closedKeyLists() {
		if len(list.values) == 0 {
			t.Errorf("%s: empty list, the guard sees nothing", list.prefix)
		}
		for _, value := range list.values {
			key := list.prefix + value + list.suffix
			for _, code := range lang.Codes() {
				if strings.HasPrefix(server.catalogs.For(code).Get(key), "[") {
					t.Errorf("la clé %q manque dans %s", key, code)
				}
			}
		}
	}
}

// closedKeyLists rend les listes fermées et leur préfixe : la seule liste des
// clés composées à partir d'une valeur du serveur, lue aussi par le test des
// clés sans lecteur.
func closedKeyLists() []composedKeys {
	return []composedKeys{
		{prefix: "probe.status_", values: names(probe.Statuses())},
		{prefix: "probe.kind_", values: names(probe.Kinds())},
		{prefix: "probe.outcome_", values: names(probe.Outcomes())},
		{prefix: "probe.reason_", values: names(probe.Reasons())},
		{prefix: "probe.window_", values: probeWindowNames()},
		{prefix: "cert.ocsp_", values: names(probe.OCSPStatuses())},
		{prefix: "job.status_", values: names(heartbeat.Statuses())},
		{prefix: "job.kind_", values: names(heartbeat.Kinds())},
		{prefix: "job.outcome_", values: names(heartbeat.Outcomes())},
		{prefix: "container.health_", values: names(container.Healths())},
		{prefix: "network.exposure_", values: names(container.FindingKinds())},
		// Les trois politiques ont leur libellé : le sélecteur de l'unité les montre toutes.
		{prefix: "update.policy_", values: names(container.UpdatePolicies())},
		// Une vérification réussie dit ce qu'elle a trouvé, pas son issue.
		{prefix: "update.outcome_", values: without(names(update.Outcomes()), string(update.OutcomeOK))},
		{prefix: "update.kind_", values: names(update.Kinds())},
		{prefix: "update.available_", values: names(update.Kinds())},
		{prefix: "lifecycle.status_", values: names(lifecycle.Statuses())},
		{prefix: "resource.window_", values: resourceWindowNames()},
		// Le libellé d'un type, sans chiffre : liste des silences, sélecteur.
		{prefix: "alert.kind_", values: names(alert.Kinds())},
		{prefix: "alert.fact_", values: names(alert.Kinds())},
		{prefix: "alert.cause_", values: names(alert.Kinds())},
		{prefix: "alert.action_", values: names(alert.Kinds())},
		{prefix: "alert.rollback_cause_", values: alert.RollbackCauses()},
		{prefix: "alert.fact_service_down_", values: alert.ServiceDownReasons()},
		{prefix: "alert.cause_service_down_", values: alert.ServiceDownReasons()},
		{prefix: "alert.action_service_down_", values: alert.ServiceDownReasons()},
		{prefix: "alert.severity_", values: names(alert.Severities())},
		{prefix: "channel.receives_", values: names(alert.Severities())},
		{prefix: "silence.target_kind_", values: names(alert.TargetKinds())},
		// Un envoi d'essai n'est jamais une livraison de la fiche.
		{prefix: "alert.event_", values: without(without(names(alert.Events()), string(alert.EventTest)), string(alert.EventSummary))},
		{prefix: "alert.notify_", values: names(alert.Events())},
		{prefix: "alert.delivery_", values: names(alert.DeliveryStatuses())},
		{prefix: "alert.delivery_reason_", values: names(alert.Reasons())},
		{prefix: "alert.reason_", values: alert.UntrustReasons()},
		{prefix: "channel.format_", values: names(alert.Formats())},
		{prefix: "settings.mail_tls_", values: names(mail.TLSModes())},
		{prefix: "status.state_", values: names(status.States())},
		// Le public lit « unknown » là où le serveur ne dit rien.
		{prefix: "status.global_", values: append(names(status.States()), "unknown")},
		{prefix: "status.global_", suffix: "_sub", values: append(names(status.States()), "unknown")},
		{prefix: "status.incident_", values: names(status.IncidentStatuses())},
		{prefix: "incident.status_", values: names(status.IncidentStatuses())},
		{prefix: "incident.impact_", values: names(status.Impacts())},
		{prefix: "component.kind_", values: names(status.MemberKinds())},
		{prefix: "language.", values: names(lang.Codes())},
		// La pastille de la carte « Accès à l'interface », par mode.
		{prefix: "auth.state_", values: names(auth.Modes())},
		// Le choix du mode à l'activation : tous sauf « none », qui est l'état de départ.
		{prefix: "auth.mode_", values: without(names(auth.Modes()), string(auth.ModeNone))},
		{prefix: "directory.nature_", values: names(directory.Natures())},
		{prefix: "directory.origin_", values: names(directory.Origins())},
		{prefix: "directory.provides_", values: names(directory.Capabilities())},
		{prefix: "directory.declared_by_", values: names(directory.Provenances())},
		{prefix: "gap.state_", values: names(directory.GapStates())},
		{prefix: "gap.reason_", values: names(directory.GapReasons())},
		{prefix: "gap.level_", values: names(container.MatchLevels())},
		{prefix: "gap.key_", values: names(directory.IgnoreKinds())},
		// Le motif pour lequel un label ne rattache le conteneur à rien.
		{prefix: "gap.orphan_", values: names(directory.LabelProblems())},
		// Le journal des fichiers et le plan : l'état d'un passage, le geste,
		// le genre d'objet, la conséquence, ce qu'une suppression emporte.
		{prefix: "manifest.state_", values: names(manifest.ApplyStates())},
		{prefix: "manifest.actor_", values: names(manifest.ActorKinds())},
		{prefix: "manifest.action_", values: names(manifest.Actions())},
		{prefix: "manifest.kind_", values: names(manifest.Kinds())},
		{prefix: "manifest.note_", values: names(manifest.Notes())},
		{prefix: "manifest.carries_", values: manifest.CarryKinds()},
		{prefix: "manifest.carries_", suffix: "_one", values: manifest.CarryKinds()},
		// Le journal d'audit : qui, le verbe d'une action, la sorte d'objet ;
		// les anciennes lignes du MCP restent lisibles.
		{prefix: "audit.actor_", values: names(append(audit.ActorKinds(), audit.LegacyActorKinds()...))},
		{prefix: "audit.verb_", values: audit.Verbs()},
		{prefix: "audit.object_", values: names(append(audit.ObjectKinds(), audit.LegacyObjectKinds()...))},
		// La carte de Paramètres sur mcp-maison : ce que sa vérification a
		// trouvé, et le conseil qui suit (aucun quand il est joignable).
		{prefix: "ai_agent.state_", values: names(mcpcheck.States())},
		{prefix: "ai_agent.hint_", values: without(names(mcpcheck.States()), string(mcpcheck.StateReachable))},
		// Le plan d'une suppression : ce qui retient, part ou se détache, compté.
		// L'état de lecture des clés SSH d'un compte.
		{prefix: "ssh.state_", values: names(sshkeys.States())},
		{prefix: "deletion.kind_", values: names(deletion.Kinds())},
		{prefix: "deletion.kind_", suffix: "_one", values: names(deletion.Kinds())},
		// Les droits d'un agent : le front remplace le point par « _ ».
		{prefix: "machine.capability_", values: capabilityKeys()},
		// Les ordres : leur genre (le point devient « _ »), l'état d'un run,
		// la raison d'un refus.
		{prefix: "action.kind_", values: orderKindKeys()},
		{prefix: "action.state_", values: names(action.States())},
		{prefix: "action.reason_", values: append(agentwire.RefusalReasons(), action.ReasonUndelivered, action.ReasonTimeout)},
		// La fiche d'une machine dit pourquoi son agent n'a pas de mise à jour à recevoir.
		{prefix: "machine.agent_update_reason_", values: action.OfferReasons()},
		// Pourquoi un droit coché n'est pas exerçable : une phrase par raison.
		{prefix: "machine.capability_unavailable_", values: machine.UnavailableReasons()},
		// Les plans de sauvegarde : d'où ils prennent, et ce qu'un agent sait produire.
		{prefix: "backup.source_kind_", values: []string{backup.SourceDatabase, backup.SourceAgent}},
		{prefix: "backup.producer_", values: machine.Producers()},
	}
}

func orderKindKeys() []string {
	out := make([]string, 0, len(agentwire.OrderKinds()))
	for _, kind := range agentwire.OrderKinds() {
		out = append(out, strings.ReplaceAll(string(kind), ".", "_"))
	}
	return out
}

func capabilityKeys() []string {
	out := make([]string, 0, len(machine.CapabilityNames()))
	for _, name := range machine.CapabilityNames() {
		out = append(out, strings.ReplaceAll(name, ".", "_"))
	}
	return out
}

func names[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func without(values []string, excluded string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != excluded {
			out = append(out, value)
		}
	}
	return out
}

func probeWindowNames() []string {
	out := make([]string, 0, len(probe.Windows))
	for _, window := range probe.Windows {
		out = append(out, window.Name)
	}
	return out
}

func resourceWindowNames() []string {
	out := make([]string, 0, len(resource.Windows))
	for _, window := range resource.Windows {
		out = append(out, window.Name)
	}
	return out
}
