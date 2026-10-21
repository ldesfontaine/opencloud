package machine

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Les droits d'un agent : cochés à l'émission du jeton, écrits par la ligne
// d'installation dans le fichier de l'agent, rapportés par lui à chaque
// ouverture de flux. Stockés et affichés ; aucun n'est encore appliqué
// (#146). observe est toujours là.
const (
	CapabilityObserve       = "observe"
	CapabilityAgentUpdate   = "agent.update"
	CapabilityDockerDeploy  = "docker.deploy"
	CapabilityDockerUpdate  = "docker.update"
	CapabilityBackupReceive = "backup.receive"
	CapabilityFilesWrite    = "files.write"
)

// CapabilityNames rend la liste fermée, dans l'ordre où elle s'affiche :
// chaque droit a son libellé dans les catalogues.
func CapabilityNames() []string {
	return []string{
		CapabilityObserve, CapabilityAgentUpdate, CapabilityDockerDeploy,
		CapabilityDockerUpdate, CapabilityBackupReceive, CapabilityFilesWrite,
	}
}

// Capability est un droit et, pour un droit borné, les chemins qu'il couvre.
type Capability struct {
	Name  string
	Paths []string
}

const (
	// La forme texte : les droits séparés par « , », un droit borné suivi de
	// « = » et de ses chemins séparés par « : ». Aucun de ces signes ni
	// espace dans un chemin : la ligne d'installation la porte sans
	// guillemets, l'en-tête de l'agent aussi.
	capabilitySeparator = ","
	scopeSeparator      = "="
	pathSeparator       = ":"
	maxCapabilityPaths  = 8
	maxCapabilityText   = 2048
)

var capabilityPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,254}$`)

var (
	ErrCapabilityUnknown = refusal.Invalid("machine.capability_unknown", "unknown agent capability")
	ErrCapabilityPath    = refusal.Invalid("machine.capability_path", "capability path must be absolute, clean, and made of letters, digits, dot, dash, underscore")
	ErrCapabilityScope   = refusal.Invalid("machine.capability_scope", "backup.receive takes one path, files.write one to eight, the others none")
)

// NormalizeCapabilities vérifie une liste de droits, ajoute observe, retire
// les doublons et la range dans l'ordre de CapabilityNames : deux listes
// égales s'écrivent pareil.
func NormalizeCapabilities(capabilities []Capability) ([]Capability, error) {
	byName := map[string]Capability{CapabilityObserve: {Name: CapabilityObserve}}
	for _, capability := range capabilities {
		if !slices.Contains(CapabilityNames(), capability.Name) {
			return nil, ErrCapabilityUnknown
		}
		paths, err := normalizePaths(capability)
		if err != nil {
			return nil, err
		}
		byName[capability.Name] = Capability{Name: capability.Name, Paths: paths}
	}
	normalized := make([]Capability, 0, len(byName))
	for _, name := range CapabilityNames() {
		if capability, ok := byName[name]; ok {
			normalized = append(normalized, capability)
		}
	}
	return normalized, nil
}

// normalizePaths juge les chemins d'un droit : un pour backup.receive, un à
// huit pour files.write, aucun pour les autres. Ils ressortent triés, sans
// doublon.
func normalizePaths(capability Capability) ([]string, error) {
	paths := slices.Clone(capability.Paths)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	for _, candidate := range paths {
		if !isCapabilityPath(candidate) {
			return nil, ErrCapabilityPath
		}
	}
	switch capability.Name {
	case CapabilityBackupReceive:
		if len(paths) != 1 {
			return nil, ErrCapabilityScope
		}
	case CapabilityFilesWrite:
		if len(paths) == 0 || len(paths) > maxCapabilityPaths {
			return nil, ErrCapabilityScope
		}
	default:
		if len(paths) != 0 {
			return nil, ErrCapabilityScope
		}
		return nil, nil
	}
	return paths, nil
}

// Un chemin absolu, déjà propre, jamais la racine : « / » voudrait dire
// toute la machine.
func isCapabilityPath(candidate string) bool {
	return capabilityPathPattern.MatchString(candidate) && path.Clean(candidate) == candidate
}

// FormatCapabilities écrit une liste déjà normalisée sous sa forme texte.
func FormatCapabilities(capabilities []Capability) string {
	items := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		items = append(items, formatCapability(capability))
	}
	return strings.Join(items, capabilitySeparator)
}

func formatCapability(capability Capability) string {
	if len(capability.Paths) == 0 {
		return capability.Name
	}
	return capability.Name + scopeSeparator + strings.Join(capability.Paths, pathSeparator)
}

// ParseCapabilities lit la forme texte et la normalise. Un texte vide vaut
// observe seul.
func ParseCapabilities(text string) ([]Capability, error) {
	if len(text) > maxCapabilityText {
		return nil, ErrCapabilityScope
	}
	var capabilities []Capability
	for item := range strings.SplitSeq(text, capabilitySeparator) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, scope, hasScope := strings.Cut(item, scopeSeparator)
		capability := Capability{Name: name}
		if hasScope {
			capability.Paths = strings.Split(scope, pathSeparator)
		}
		capabilities = append(capabilities, capability)
	}
	return NormalizeCapabilities(capabilities)
}

// SameCapabilities dit si deux listes normalisées disent la même chose.
func SameCapabilities(first, second []Capability) bool {
	return FormatCapabilities(first) == FormatCapabilities(second)
}
