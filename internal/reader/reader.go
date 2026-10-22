package reader

import (
	"context"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// Reader est ce que le serveur, la page de statut et le fichier de
// l'annuaire lisent de la base sans passer par un composant : une liste,
// un objet, un compte. Rien ici ne vérifie ni ne publie ; une lecture qui
// porte une règle (une fenêtre calculée, un filtre du composant) reste
// chez son composant. Les domaines qui lisent par ce Reader (statut,
// annuaire par le fichier) n'y rendent pas leurs propres types : Go
// refuserait le cycle d'imports.
type Reader interface {
	// MachineNames rend le nom de chaque machine, par identifiant.
	MachineNames(ctx context.Context) (map[string]string, error)

	// ListContainers rend les fiches d'une machine, archivées comprises ;
	// toutes les machines si machineID est vide.
	ListContainers(ctx context.Context, machineID string) ([]container.Container, error)
	GetContainer(ctx context.Context, id string) (container.Container, error)
	ListEngines(ctx context.Context) ([]container.Engine, error)
	// CountContainers compte les conteneurs vivants et ceux qui demandent
	// attention (container.NeedsAttention, écrit en SQL).
	CountContainers(ctx context.Context) (total, attention int, err error)
	// CountContainerUpdates compte les conteneurs vivants qui ont une mise à
	// jour et dont l'unité veut la voir (politique « watch »).
	CountContainerUpdates(ctx context.Context) (int, error)

	// ListProbes rend les sondes d'une machine, toutes si machineID est vide.
	ListProbes(ctx context.Context, machineID string) ([]probe.Probe, error)
	GetProbe(ctx context.Context, id string) (probe.Probe, error)
	// CountProbes compte les sondes et celles à traiter : hors ligne.
	CountProbes(ctx context.Context) (total, attention int, err error)
	ListProbeResults(ctx context.Context, probeID string, limit int) ([]probe.Result, error)
	// ListProbeDays rend les jours d'une sonde depuis l'instant donné, ou
	// de toutes si probeID est vide.
	ListProbeDays(ctx context.Context, probeID string, from time.Time) ([]probe.Day, error)

	ListHeartbeats(ctx context.Context) ([]heartbeat.Heartbeat, error)
	GetHeartbeat(ctx context.Context, id string) (heartbeat.Heartbeat, error)
	// CountHeartbeats compte les moniteurs et ceux à traiter : en retard
	// ou en échec.
	CountHeartbeats(ctx context.Context) (total, attention int, err error)
	ListPings(ctx context.Context, heartbeatID string, limit int) ([]heartbeat.Ping, error)
	ListRuns(ctx context.Context, heartbeatID string, limit int) ([]heartbeat.Run, error)

	ListDeclaredServices(ctx context.Context, filter directory.Filter) ([]directory.Service, error)
	GetDeclaredService(ctx context.Context, id string) (directory.Service, error)
	// ListServiceLinks rend les sondes et les composants de statut qui
	// visent le service.
	ListServiceLinks(ctx context.Context, serviceID string) (directory.Links, error)
	ListProjects(ctx context.Context) ([]directory.Project, error)
	GetProject(ctx context.Context, id string) (directory.Project, error)
	// ListIgnoredKeys rend les clés ignorées d'une machine, de toutes si
	// machineID est vide.
	ListIgnoredKeys(ctx context.Context, machineID string) ([]directory.IgnoredKey, error)

	ListAlerts(ctx context.Context, filter alert.Filter) ([]alert.Alert, error)
	GetAlert(ctx context.Context, id int64) (alert.Alert, error)
	CountAlerts(ctx context.Context) (alert.Counts, error)
	ListDeliveries(ctx context.Context, alertID int64) ([]alert.Delivery, error)
	ListChannels(ctx context.Context) ([]alert.Channel, error)
	GetChannel(ctx context.Context, id int64) (alert.Channel, error)
	ListMutes(ctx context.Context) ([]alert.Mute, error)

	// LoadSettings rend des réglages vides tant que rien n'est réglé.
	LoadSettings(ctx context.Context) (settings.Settings, error)

	// ListAuditEvents rend une page du journal, du plus récent au plus ancien.
	ListAuditEvents(ctx context.Context, query audit.Query) ([]audit.Event, error)

	// CountAuditForwarding dit combien de canaux reçoivent le journal et
	// combien de ses livraisons sont en échec.
	CountAuditForwarding(ctx context.Context) (channels int, failing int, err error)

	// ListActionRuns rend les ordres envoyés aux agents, d'une machine ou de
	// toutes, du plus récent au plus ancien, avec leur sortie.
	ListActionRuns(ctx context.Context, query action.Query) ([]action.Run, error)
	// GetActionRun rend un ordre et sa sortie ; sql.ErrNoRows, enveloppé,
	// s'il n'existe plus.
	GetActionRun(ctx context.Context, id string) (action.Run, error)
}
