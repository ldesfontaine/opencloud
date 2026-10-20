package directory

import (
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Nature dit si le service est ce que l'on déploie pour ses utilisateurs ou
// ce qui sert les autres services.
type Nature string

const (
	NatureApplication Nature = "application"
	NatureInfra       Nature = "infra"
)

func Natures() []Nature {
	return []Nature{NatureApplication, NatureInfra}
}

// Origin dit comment le service tourne, donc comment on le retrouvera dans
// ce que la machine montre : un projet Compose, un conteneur seul, un
// binaire sous systemd. « deployed » (déployé depuis l'application) est réservé à
// l'étape qui saura le faire : il est refusé d'ici là.
type Origin string

const (
	OriginCompose   Origin = "compose"
	OriginContainer Origin = "container"
	OriginBinary    Origin = "binary"
)

func Origins() []Origin {
	return []Origin{OriginCompose, OriginContainer, OriginBinary}
}

// UnitKind est le genre d'une unité ; il se déduit de l'origine du service.
type UnitKind string

const (
	UnitContainer UnitKind = "container"
	UnitBinary    UnitKind = "binary"
)

// Capability est ce qu'un service d'infrastructure fournit aux autres.
type Capability string

const (
	CapabilitySSO          Capability = "sso"
	CapabilityProxy        Capability = "proxy"
	CapabilityTLS          Capability = "tls"
	CapabilityBackupTarget Capability = "backup-target"
)

func Capabilities() []Capability {
	return []Capability{CapabilitySSO, CapabilityProxy, CapabilityTLS, CapabilityBackupTarget}
}

// DeclaredBy dit d'où vient la déclaration : de l'opérateur, ou de la
// reprise de l'existant (migration).
type DeclaredBy string

const (
	ByOperator  DeclaredBy = "operator"
	ByMigration DeclaredBy = "migration"
)

func Provenances() []DeclaredBy {
	return []DeclaredBy{ByOperator, ByMigration}
}

const (
	ServicePrefix = "svc_"
	UnitPrefix    = "unit_"
	ProjectPrefix = "prj_"

	MaxTitleLength     = 80
	MaxPublicURLLength = 2048
	MaxImageLength     = 512
	MaxUnits           = 32
	MaxPorts           = 64
)

var (
	ErrNotFound = errors.New("declared service not found")

	ErrMachineInvalid        = refusal.Invalid("directory.machine_invalid", "declared service machine must exist")
	ErrNameInvalid           = refusal.Invalid("directory.name_invalid", "declared service name must be a lowercase label: letters, digits, dashes, underscores")
	ErrTitleInvalid          = refusal.Invalid("directory.title_invalid", "declared service title must be at most 80 characters")
	ErrNatureInvalid         = refusal.Invalid("directory.nature_invalid", "declared service nature must be application or infra")
	ErrOriginInvalid         = refusal.Invalid("directory.origin_invalid", "declared service origin must be compose, container or binary")
	ErrComposeProjectInvalid = refusal.Invalid("directory.compose_project_invalid", "compose project is required for a compose service, and only for it")
	ErrPublicURLInvalid      = refusal.Invalid("directory.public_url_invalid", "public url must be an absolute http or https address")
	ErrProvidesInvalid       = refusal.Invalid("directory.provides_invalid", "a service can only provide sso, proxy, tls or backup-target")
	ErrProvidesNeedInfra     = refusal.Invalid("directory.provides_need_infra", "only an infrastructure service provides a capability")
	ErrUnitsInvalid          = refusal.Invalid("directory.units_invalid", "a service has from 1 to 32 units")
	ErrUnitNameInvalid       = refusal.Invalid("directory.unit_name_invalid", "unit name is not valid")
	ErrUnitNameTaken         = refusal.Invalid("directory.unit_name_taken", "unit name already used in this service")
	ErrImageInvalid          = refusal.Invalid("directory.image_invalid", "unit image must be a reference without tag or digest, and only for a container")
	ErrVersionInvalid        = refusal.Invalid("directory.version_invalid", "unit version is not a valid tag")
	ErrVersionLatest         = refusal.Invalid("directory.version_latest", "unit version cannot be latest")
	ErrDigestInvalid         = refusal.Invalid("directory.digest_invalid", "unit digest must be sha256 followed by 64 hexadecimal digits, and only for an image")
	ErrContainerNameInvalid  = refusal.Invalid("directory.container_name_invalid", "container name is required for a container service, and only for it")
	ErrSystemdUnitInvalid    = refusal.Invalid("directory.systemd_unit_invalid", "systemd unit is required for a binary service, and only for it")
	ErrUnitKeyDuplicate      = refusal.Invalid("directory.unit_key_duplicate", "two units of the service declare the same container name or systemd unit")
	ErrPortsInvalid          = refusal.Invalid("directory.ports_invalid", "unit ports must be port, host:port, with an optional /tcp or /udp")
	ErrNameTaken             = refusal.Invalid("directory.name_taken", "declared service name already used on this machine")
	ErrComposeProjectTaken   = refusal.Invalid("directory.compose_project_taken", "compose project already declared on this machine")
	ErrMatchKeyTaken         = refusal.Invalid("directory.match_key_taken", "container name or systemd unit already declared on this machine")
	ErrUnitInvalid           = refusal.Invalid("directory.unit_invalid", "unit does not belong to this service")
	ErrProjectInvalid        = refusal.Invalid("directory.project_invalid", "project must exist")
	ErrProjectNotFound       = errors.New("project not found")
	ErrProjectNameInvalid    = refusal.Invalid("project.name_invalid", "project name must be a lowercase label: letters, digits, dashes, underscores")
	ErrProjectTitleInvalid   = refusal.Invalid("project.title_invalid", "project title must be at most 80 characters")
	ErrProjectNameTaken      = refusal.Invalid("project.name_taken", "project name already used")

	ErrIgnoreInvalid     = refusal.Invalid("directory.ignore_invalid", "ignored key needs an existing machine, a known kind and a value that fits it")
	ErrIgnoredNotFound   = errors.New("ignored key not found")
	ErrContainerNotFound = errors.New("container not found, or already gone")
	ErrContainerDeclared = refusal.Conflict("directory.container_declared", "container already belongs to a declared service")
	// Le conteneur porte un label qui ne vise aucune unité de sa machine :
	// l'adopter créerait une unité que ce label empêcherait de rattacher.
	// Un état que l'opérateur corrige par le label, d'où un conflit.
	ErrContainerLabelled = refusal.Conflict("directory.container_labelled", "container carries a label that targets no unit of its machine")
	// Le service du projet Compose a déjà MaxUnits unités : rien ne s'y
	// ajoute. Un état du service, pas une faute de saisie.
	ErrUnitsFull = refusal.Conflict("directory.units_full", "declared service already holds the maximum number of units")

	ErrUpdatePolicyInvalid = refusal.Invalid("directory.update_policy_invalid", "unit update policy must be watch, hold or ignore, and only watch for a binary")
	ErrUnitNotFound        = errors.New("unit not found")
	// ErrServiceLinked : des sondes ou des composants de statut visent le
	// service. LinkedError porte la liste et enveloppe ce refus.
	ErrServiceLinked = refusal.Conflict("directory.service_linked", "declared service is still watched or shown")
)

// Service est un service tel que l'opérateur l'a déclaré, avec ses unités.
type Service struct {
	ID          string
	MachineID   string
	MachineName string
	Name        string
	Title       string
	Nature      Nature
	Origin      Origin
	// Le projet Compose du service, seulement en origine compose.
	ComposeProject string
	PublicURL      string
	DeclaredBy     DeclaredBy
	NeedsReview    bool
	Provides       []Capability
	Units          []Unit
	ProjectIDs     []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Unit est une pièce du service : un conteneur ou un binaire. Pour un
// service Compose, son nom est celui du service dans le projet.
type Unit struct {
	ID            string
	ServiceID     string
	Name          string
	Kind          UnitKind
	Image         string
	Version       string
	Digest        string
	ContainerName string
	SystemdUnit   string
	Ports         []string
	// Ce que l'opérateur veut des mises à jour de l'image ; elle vaut pour
	// tous les conteneurs de l'unité, recréés compris.
	UpdatePolicy container.UpdatePolicy
}

// Project regroupe des services. Le lien est le seul que le projet porte :
// ni l'un ni l'autre ne disparaît avec l'autre.
type Project struct {
	ID         string
	Name       string
	Title      string
	ServiceIDs []string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ServiceDefinition est ce que l'opérateur donne pour déclarer un service,
// ou pour remplacer sa définition entière.
type ServiceDefinition struct {
	MachineID      string
	Name           string
	Title          string
	Nature         Nature
	Origin         Origin
	ComposeProject string
	PublicURL      string
	Provides       []Capability
	Units          []UnitDefinition
	ProjectIDs     []string
}

// UnitDefinition décrit une unité. Un ID vide est une unité nouvelle ; à
// l'édition, un ID gardé garde l'unité, et celles qui manquent sont
// supprimées.
type UnitDefinition struct {
	ID   string
	Name string
	// Kind est déduit de l'origine par Complete : l'opérateur ne le dit pas.
	Kind          UnitKind
	Image         string
	Version       string
	Digest        string
	ContainerName string
	SystemdUnit   string
	Ports         []string
	// Vide : Complete la pose à watch.
	UpdatePolicy container.UpdatePolicy
}

type ProjectDefinition struct {
	Name  string
	Title string
}

// Filter restreint la liste des services ; un champ vide ne restreint pas.
type Filter struct {
	MachineID string
	ProjectID string
}

// LinkedObject est un objet qui vise un service déclaré : une sonde ou un
// composant de statut.
type LinkedObject struct {
	ID   string
	Name string
}

// Links dit ce qui vise un service déclaré. Tant qu'il n'est pas vide, le
// service ne se supprime pas.
type Links struct {
	Probes     []LinkedObject
	Components []LinkedObject
}

func (l Links) IsEmpty() bool {
	return len(l.Probes) == 0 && len(l.Components) == 0
}

// LinkedError est le refus de supprimer un service que des sondes ou des
// composants visent encore, avec la liste de ce qui le lie.
type LinkedError struct {
	Links Links
}

func (e *LinkedError) Error() string {
	return ErrServiceLinked.Error()
}

// Unwrap rend le refus : errors.Is le reconnaît, et refusal.KeyOf y lit la
// clé, pour le fichier de l'annuaire qui n'a pas besoin de la liste.
func (e *LinkedError) Unwrap() error {
	return ErrServiceLinked
}

// NewID tire un identifiant du serveur : le préfixe de l'objet, puis
// 16 hexadécimaux. Jamais un identifiant Docker.
func NewID(prefix string) (string, error) {
	id, err := ident.New()
	if err != nil {
		return "", err
	}
	return prefix + id, nil
}

// unitKindOf : un binaire sous systemd est une unité binaire, tout le reste
// tourne en conteneur.
func unitKindOf(origin Origin) UnitKind {
	if origin == OriginBinary {
		return UnitBinary
	}
	return UnitContainer
}
