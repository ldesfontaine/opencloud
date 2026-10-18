package directory

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
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
	idBytes            = 8
)

var (
	ErrNotFound = errors.New("declared service not found")

	ErrMachineInvalid        = errors.New("declared service machine must exist")
	ErrNameInvalid           = errors.New("declared service name must be a lowercase label: letters, digits, dashes, underscores")
	ErrTitleInvalid          = errors.New("declared service title must be at most 80 characters")
	ErrNatureInvalid         = errors.New("declared service nature must be application or infra")
	ErrOriginInvalid         = errors.New("declared service origin must be compose, container or binary")
	ErrComposeProjectInvalid = errors.New("compose project is required for a compose service, and only for it")
	ErrPublicURLInvalid      = errors.New("public url must be an absolute http or https address")
	ErrProvidesInvalid       = errors.New("a service can only provide sso, proxy, tls or backup-target")
	ErrProvidesNeedInfra     = errors.New("only an infrastructure service provides a capability")
	ErrUnitsInvalid          = errors.New("a service has from 1 to 32 units")
	ErrUnitNameInvalid       = errors.New("unit name is not valid")
	ErrUnitNameTaken         = errors.New("unit name already used in this service")
	ErrImageInvalid          = errors.New("unit image must be a reference without tag or digest, and only for a container")
	ErrVersionInvalid        = errors.New("unit version is not a valid tag")
	ErrVersionLatest         = errors.New("unit version cannot be latest")
	ErrDigestInvalid         = errors.New("unit digest must be sha256 followed by 64 hexadecimal digits, and only for an image")
	ErrContainerNameInvalid  = errors.New("container name is required for a container service, and only for it")
	ErrSystemdUnitInvalid    = errors.New("systemd unit is required for a binary service, and only for it")
	ErrUnitKeyDuplicate      = errors.New("two units of the service declare the same container name or systemd unit")
	ErrPortsInvalid          = errors.New("unit ports must be port, host:port, with an optional /tcp or /udp")
	ErrNameTaken             = errors.New("declared service name already used on this machine")
	ErrComposeProjectTaken   = errors.New("compose project already declared on this machine")
	ErrMatchKeyTaken         = errors.New("container name or systemd unit already declared on this machine")
	ErrUnitInvalid           = errors.New("unit does not belong to this service")
	ErrProjectInvalid        = errors.New("project must exist")
	ErrProjectNotFound       = errors.New("project not found")
	ErrProjectNameInvalid    = errors.New("project name must be a lowercase label: letters, digits, dashes, underscores")
	ErrProjectTitleInvalid   = errors.New("project title must be at most 80 characters")
	ErrProjectNameTaken      = errors.New("project name already used")
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

// NewID tire un identifiant du serveur : le préfixe de l'objet, puis
// 16 hexadécimaux. Jamais un identifiant Docker.
func NewID(prefix string) (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(raw), nil
}

// unitKindOf : un binaire sous systemd est une unité binaire, tout le reste
// tourne en conteneur.
func unitKindOf(origin Origin) UnitKind {
	if origin == OriginBinary {
		return UnitBinary
	}
	return UnitContainer
}
