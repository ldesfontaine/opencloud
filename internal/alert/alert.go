package alert

import (
	"errors"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Severity : deux gravités, pas trois. Un ton de plus ne dirait rien de
// plus à qui doit agir.
type Severity string

const (
	SeverityAttention Severity = "attention"
	SeverityDanger    Severity = "danger"
)

// rank ordonne les gravités : une alerte ne s'aggrave que vers le haut.
func rank(severity Severity) int {
	if severity == SeverityDanger {
		return 2
	}
	return 1
}

func Severities() []Severity {
	return []Severity{SeverityAttention, SeverityDanger}
}

func isSeverity(severity Severity) bool {
	return slices.Contains(Severities(), severity)
}

// Kind est le catalogue fermé du premier lot : chaque type a sa source, sa
// condition d'ouverture et sa condition de reprise, écrites dans
// docs/projet/22-fonctionnement.md. La sauvegarde en échec attend les
// sauvegardes.
type Kind string

const (
	KindMachineLost      Kind = "machine_lost"
	KindServiceDown      Kind = "service_down"
	KindServiceUnhealthy Kind = "service_unhealthy"
	KindServiceRestart   Kind = "service_restart"
	KindJobLate          Kind = "job_late"
	KindJobFailed        Kind = "job_failed"
	KindProbeDown        Kind = "probe_down"
	KindCertExpiring     Kind = "cert_expiring"
	KindCertExpired      Kind = "cert_expired"
	KindCertUntrusted    Kind = "cert_untrusted"
	KindDiskFull         Kind = "disk_full"
)

// Kinds liste le catalogue, dans l'ordre des écrans.
func Kinds() []Kind {
	return []Kind{
		KindMachineLost, KindServiceDown, KindServiceUnhealthy, KindServiceRestart,
		KindJobLate, KindJobFailed, KindProbeDown,
		KindCertExpiring, KindCertExpired, KindCertUntrusted, KindDiskFull,
	}
}

func isKind(kind Kind) bool {
	for _, known := range Kinds() {
		if known == kind {
			return true
		}
	}
	return false
}

type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
)

// ObjectKind est le genre de l'objet qu'une alerte concerne. Un volume est
// un point de montage d'une machine ; le certificat n'est pas un objet, il
// est un fait de sa sonde. Le service déclaré n'est pas l'objet d'une
// alerte de conteneur : chaque conteneur a son état, l'alerte dit à quel
// service il appartient (Fact.ServiceID).
type ObjectKind string

const (
	ObjectMachine   ObjectKind = "machine"
	ObjectContainer ObjectKind = "container"
	ObjectHeartbeat ObjectKind = "heartbeat"
	ObjectProbe     ObjectKind = "probe"
	ObjectVolume    ObjectKind = "volume"
)

func ObjectKinds() []ObjectKind {
	return []ObjectKind{ObjectMachine, ObjectContainer, ObjectHeartbeat, ObjectProbe, ObjectVolume}
}

func isObjectKind(kind ObjectKind) bool {
	return slices.Contains(ObjectKinds(), kind)
}

// Object désigne ce dont l'alerte parle. Le nom est gardé avec l'alerte :
// un objet supprimé reste lisible dans l'historique.
type Object struct {
	Kind ObjectKind
	ID   string
	Name string
}

// VolumeID dérive l'identifiant d'un volume : la même machine et le même
// point de montage tombent sur la même alerte.
func VolumeID(machineID, mountPoint string) string {
	return machineID + ":" + mountPoint
}

// Details sont les chiffres que le fait porte ; le navigateur et le canal
// en font une phrase dans leur langue. Des valeurs, jamais des pointeurs :
// deux détails se comparent, et un détail inchangé ne réveille personne.
type Details struct {
	// Redémarrages comptés dans la fenêtre.
	Count int `json:"count,omitempty"`
	// Remplissage d'un volume, en pourcentage entier.
	Percent int `json:"percent,omitempty"`
	// Le point de montage d'un volume.
	MountPoint string `json:"mount_point,omitempty"`
	// Le seuil d'attention en vigueur quand le volume a été jugé : la
	// phrase cite celui-là, celui qui a fait ouvrir ou tenir l'alerte.
	Threshold int `json:"threshold,omitempty"`
	// Le code de sortie d'un service ou d'une tâche ; 0 vaut « aucun »,
	// une fin à 0 n'alerte jamais.
	ExitCode int `json:"exit_code,omitempty"`
	// Un mot d'une liste fermée : le motif d'échec d'une sonde, ce qui rend
	// un certificat non vérifié (chain, hostname, revoked), oom pour un
	// service tué faute de mémoire, killed pour un service tué sans arrêt
	// demandé.
	Reason string `json:"reason,omitempty"`
	// La cible d'une sonde, ou l'hôte d'un certificat.
	Target string `json:"target,omitempty"`
	// L'échéance d'un certificat.
	NotAfter time.Time `json:"not_after,omitzero"`
	// Depuis quand une machine ne donne plus signe de vie.
	Since time.Time `json:"since,omitzero"`
}

func (d Details) Equal(other Details) bool {
	return d.Count == other.Count && d.Percent == other.Percent && d.MountPoint == other.MountPoint && d.Threshold == other.Threshold &&
		d.ExitCode == other.ExitCode && d.Reason == other.Reason && d.Target == other.Target &&
		d.NotAfter.Equal(other.NotAfter) && d.Since.Equal(other.Since)
}

// Fact est ce qu'une source émet : de quoi ouvrir une alerte, ou en
// aggraver une. La clé de dédup est (type, objet).
type Fact struct {
	Kind      Kind
	Severity  Severity
	Object    Object
	MachineID string
	// ServiceID : le service déclaré auquel l'objet appartient, l'unité d'un
	// conteneur ou le service qu'une sonde surveille ; vide sinon.
	ServiceID string
	Details   Details
	// ClearSince : depuis quand l'état est redevenu bon ; zéro tant que le
	// problème dure. Seuls les services s'en servent.
	ClearSince time.Time
}

// isCrash dit si le type est l'un des deux visages d'un service qui
// plante : arrêté, ou relancé après un arrêt. Un service n'a qu'une
// alerte vivante de cette nature, dont le type suit l'état du moment.
func isCrash(kind Kind) bool {
	return kind == KindServiceDown || kind == KindServiceRestart
}

// isLoop dit qu'un plantage est devenu une boucle : le seuil de
// redémarrages est atteint dans la fenêtre.
func isLoop(kind Kind, details Details) bool {
	return isCrash(kind) && details.Count >= RestartLoopThreshold
}

// Alert est une alerte telle que la base la connaît. Les dates zéro
// disent « jamais ».
type Alert struct {
	ID       int64
	Kind     Kind
	Severity Severity
	Status   Status
	Object   Object
	// La machine concernée, celle de l'objet ; vide pour une tâche sans
	// machine. Le nom se lit à la lecture, la machine retirée n'en a plus.
	MachineID   string
	MachineName string
	// Le service déclaré de l'objet ; vide pour un conteneur non déclaré.
	ServiceID string
	Details   Details
	OpenedAt  time.Time
	// UpdatedAt bouge à chaque aggravation ou détail nouveau.
	UpdatedAt      time.Time
	ResolvedAt     time.Time
	AcknowledgedAt time.Time
	// ClearSince : depuis quand l'état d'un service est redevenu bon. Zéro
	// tant que le problème dure ; l'alerte se résout passé ServiceClearDelay.
	ClearSince time.Time
	// Muted : une fenêtre pour taire la couvre en ce moment. Jamais en base :
	// le moteur le calcule à la lecture.
	Muted bool
}

func (a Alert) IsOpen() bool {
	return a.Status == StatusOpen
}

func (a Alert) IsLoop() bool {
	return isLoop(a.Kind, a.Details)
}

func (a Alert) IsAcknowledged() bool {
	return !a.AcknowledgedAt.IsZero()
}

// Filter dit quelles alertes lire ; un champ vide ne filtre pas.
type Filter struct {
	Status     Status
	Kind       Kind
	ObjectKind ObjectKind
	ObjectID   string
	MachineID  string
	// UpdatedBefore ne garde que ce qui n'a pas bougé depuis.
	UpdatedBefore time.Time
	// ClearBefore ne garde que ce qui est redevenu bon depuis au moins cet
	// instant.
	ClearBefore time.Time
	Limit       int
}

// Counts est ce que la barre latérale et la vue d'ensemble comptent.
type Counts struct {
	Open int
	// Unacknowledged : les ouvertes que personne n'a encore acquittées ;
	// c'est le compteur rouge.
	Unacknowledged int
}

const (
	// Une machine est perdue passé ce délai sans signal : quatre signaux
	// manqués, pas un hoquet de réseau.
	MachineLostAfter = 2 * time.Minute
	// Les redémarrages non demandés se comptent sur cette fenêtre ; au
	// troisième, c'est une boucle. Le même délai de calme résout l'alerte.
	RestartWindow        = 10 * time.Minute
	RestartLoopThreshold = 3
	// Un service arrêté ou défaillant ne se résout qu'après être resté bon
	// ce temps-là : un conteneur qui repart pour retomber garde la même
	// alerte, au lieu d'en ouvrir et d'en résoudre une à chaque tour.
	ServiceClearDelay = 2 * time.Minute
	// Un canal reçoit au plus ChannelRateLimit envois sur ChannelRateWindow ;
	// le surplus est retenu et résumé en un seul message. Chaque machine y
	// a sa part, MachineRateLimit : une machine bruyante, ou qui ment,
	// n'épuise que la sienne. ChannelReserve est ce qui reste de place, au-delà
	// de ChannelRateLimit, à la première alerte de chaque machine silencieuse
	// sur la fenêtre : même si d'autres ont rempli le canal, elle passe.
	ChannelRateLimit  = 10
	ChannelRateWindow = 5 * time.Minute
	MachineRateLimit  = 5
	ChannelReserve    = 5
	// Un résumé nomme au plus ces machines, puis compte les autres.
	MaxSummaryMachines = 3
	// Les seuils du disque tant que l'opérateur n'en a pas réglé d'autres :
	// voir disk.go.
	DefaultDiskAttentionPercent = 85
	DefaultDiskDangerPercent    = 95
	// Combien de temps une alerte résolue reste lisible.
	ResolvedRetention = 90 * 24 * time.Hour
	// Ce que la page liste d'un coup.
	MaxListed = 200
	// Un acquittement groupé couvre au plus ce que la page affiche.
	MaxAcknowledgedAtOnce = MaxListed
)

var (
	ErrNotFound      = errors.New("alert: not found")
	ErrAlreadyClosed = refusal.Conflict("alert.already_resolved", "alert: already resolved")
	ErrFactInvalid   = errors.New("alert: fact invalid")
	// Un acquittement groupé sans alerte, ou au-delà de ce que la page
	// affiche.
	ErrSelectionEmpty    = refusal.Invalid("alert.selection_empty", "alert: selection empty")
	ErrSelectionTooLarge = refusal.Invalid("alert.selection_too_large", "alert: selection too large")
)

// Validate refuse un fait sans objet : la clé de dédup serait vide et deux
// objets se partageraient une alerte. C'est le garde-fou de collision.
func (f Fact) Validate() error {
	if !isKind(f.Kind) || !isSeverity(f.Severity) || !isObjectKind(f.Object.Kind) || f.Object.ID == "" {
		return ErrFactInvalid
	}
	return nil
}
