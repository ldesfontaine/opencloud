package alert

import (
	"errors"
	"slices"
	"time"
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
// est un fait de sa sonde.
type ObjectKind string

const (
	ObjectMachine   ObjectKind = "machine"
	ObjectService   ObjectKind = "service"
	ObjectHeartbeat ObjectKind = "heartbeat"
	ObjectProbe     ObjectKind = "probe"
	ObjectVolume    ObjectKind = "volume"
)

func ObjectKinds() []ObjectKind {
	return []ObjectKind{ObjectMachine, ObjectService, ObjectHeartbeat, ObjectProbe, ObjectVolume}
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
	// Le code de sortie d'un service ou d'une tâche ; 0 vaut « aucun »,
	// une fin à 0 n'alerte jamais.
	ExitCode int `json:"exit_code,omitempty"`
	// Un mot d'une liste fermée : le motif d'échec d'une sonde, ou ce qui
	// rend un certificat non vérifié (chain, hostname, revoked).
	Reason string `json:"reason,omitempty"`
	// La cible d'une sonde, ou l'hôte d'un certificat.
	Target string `json:"target,omitempty"`
	// L'échéance d'un certificat.
	NotAfter time.Time `json:"not_after,omitzero"`
	// Depuis quand une machine ne donne plus signe de vie.
	Since time.Time `json:"since,omitzero"`
}

func (d Details) Equal(other Details) bool {
	return d.Count == other.Count && d.Percent == other.Percent && d.MountPoint == other.MountPoint &&
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
	Details   Details
}

// Alert est une alerte telle que la base la connaît. Les dates zéro
// disent « jamais ».
type Alert struct {
	ID       int64
	Kind     Kind
	Severity Severity
	Status   Status
	// Silenced : ouverte sous un silence ou une maintenance, montrée mais
	// jamais livrée, même quand le silence tombe.
	Silenced bool
	Object   Object
	// La machine concernée, celle de l'objet ; vide pour une tâche sans
	// machine. Le nom se lit à la lecture, la machine retirée n'en a plus.
	MachineID   string
	MachineName string
	Details     Details
	OpenedAt    time.Time
	// UpdatedAt bouge à chaque aggravation ou détail nouveau : c'est sur
	// lui qu'un redémarrage non demandé finit par se résoudre.
	UpdatedAt      time.Time
	ResolvedAt     time.Time
	AcknowledgedAt time.Time
}

func (a Alert) IsOpen() bool {
	return a.Status == StatusOpen
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
	Limit         int
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
	// Un volume alerte au premier seuil, s'aggrave au second, et ne se
	// résout que sous le seuil de retour : sinon ça clignote.
	DiskAttentionPercent = 85
	DiskDangerPercent    = 95
	DiskRecoveryPercent  = 80
	// Combien de temps une alerte résolue reste lisible.
	ResolvedRetention = 90 * 24 * time.Hour
	// Ce que la page liste d'un coup.
	MaxListed = 200
)

var (
	ErrNotFound      = errors.New("alert: not found")
	ErrAlreadyClosed = errors.New("alert: already resolved")
	ErrFactInvalid   = errors.New("alert: fact invalid")
)

// Validate refuse un fait sans objet : la clé de dédup serait vide et deux
// objets se partageraient une alerte. C'est le garde-fou de collision.
func (f Fact) Validate() error {
	if !isKind(f.Kind) || !isSeverity(f.Severity) || !isObjectKind(f.Object.Kind) || f.Object.ID == "" {
		return ErrFactInvalid
	}
	return nil
}
