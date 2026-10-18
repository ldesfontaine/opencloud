package server

import (
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/service"
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
	lists := []composedKeys{
		{prefix: "probe.status_", values: names(probe.Statuses())},
		{prefix: "probe.kind_", values: names(probe.Kinds())},
		{prefix: "probe.outcome_", values: names(probe.Outcomes())},
		{prefix: "probe.reason_", values: names(probe.Reasons())},
		{prefix: "probe.window_", values: probeWindowNames()},
		{prefix: "cert.ocsp_", values: names(probe.OCSPStatuses())},
		{prefix: "job.status_", values: names(heartbeat.Statuses())},
		{prefix: "job.kind_", values: names(heartbeat.Kinds())},
		{prefix: "job.outcome_", values: names(heartbeat.Outcomes())},
		{prefix: "service.health_", values: names(service.Healths())},
		{prefix: "network.exposure_", values: names(service.FindingKinds())},
		// Suivre est la politique par défaut, sans pastille.
		{prefix: "update.policy_", values: without(names(service.UpdatePolicies()), "")},
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
		{prefix: "alert.fact_service_down_", values: alert.ServiceDownReasons()},
		{prefix: "alert.cause_service_down_", values: alert.ServiceDownReasons()},
		{prefix: "alert.action_service_down_", values: alert.ServiceDownReasons()},
		{prefix: "alert.severity_", values: names(alert.Severities())},
		{prefix: "channel.receives_", values: names(alert.Severities())},
		{prefix: "silence.object_kind_", values: names(alert.ObjectKinds())},
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
		{prefix: "mcp.scope_", values: names(mcp.Scopes())},
		// La pastille de la carte « Accès à l'interface », par mode.
		{prefix: "auth.state_", values: names(auth.Modes())},
		{prefix: "directory.nature_", values: names(directory.Natures())},
		{prefix: "directory.origin_", values: names(directory.Origins())},
		{prefix: "directory.provides_", values: names(directory.Capabilities())},
		{prefix: "directory.declared_by_", values: names(directory.Provenances())},
	}
	for _, list := range lists {
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
