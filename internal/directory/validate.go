package directory

import (
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/registry"
)

var (
	namePattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	unitNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
	versionPattern       = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestPattern        = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	containerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
	systemdUnitPattern   = regexp.MustCompile(`^[A-Za-z0-9:_.@-]{1,250}\.service$`)
	portPattern          = regexp.MustCompile(`^(?:([0-9]{1,5}):)?([0-9]{1,5})(?:/(?:tcp|udp))?$`)
	dataPathPattern      = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,254}$`)
)

// Complete pose ce que l'opérateur n'a pas à dire : les espaces en trop
// sont retirés, les doublons de fournitures et de projets aussi, et le
// genre de chaque unité se déduit de l'origine du service.
func (d ServiceDefinition) Complete() ServiceDefinition {
	d.MachineID = strings.TrimSpace(d.MachineID)
	d.Name = strings.TrimSpace(d.Name)
	d.Title = strings.TrimSpace(d.Title)
	d.ComposeProject = strings.TrimSpace(d.ComposeProject)
	d.PublicURL = strings.TrimSpace(d.PublicURL)
	d.Provides = withoutDuplicates(d.Provides)
	d.ProjectIDs = withoutDuplicates(d.ProjectIDs)
	units := make([]UnitDefinition, 0, len(d.Units))
	for _, unit := range d.Units {
		units = append(units, unit.complete(unitKindOf(d.Origin)))
	}
	d.Units = units
	return d
}

func (u UnitDefinition) complete(kind UnitKind) UnitDefinition {
	u.ID = strings.TrimSpace(u.ID)
	u.Name = strings.TrimSpace(u.Name)
	u.Kind = kind
	u.Image = strings.TrimSpace(u.Image)
	u.Version = strings.TrimSpace(u.Version)
	u.Digest = strings.TrimSpace(u.Digest)
	u.ContainerName = strings.TrimSpace(u.ContainerName)
	u.SystemdUnit = strings.TrimSpace(u.SystemdUnit)
	u.DataPath = strings.TrimSpace(u.DataPath)
	ports := make([]string, 0, len(u.Ports))
	for _, port := range u.Ports {
		ports = append(ports, strings.TrimSpace(port))
	}
	u.Ports = ports
	u.UpdatePolicy = container.UpdatePolicy(strings.TrimSpace(string(u.UpdatePolicy)))
	if u.UpdatePolicy == "" {
		u.UpdatePolicy = container.PolicyWatch
	}
	return u
}

// withoutDuplicates garde la première occurrence de chaque valeur, dans
// l'ordre donné ; les espaces autour d'une valeur sont retirés.
func withoutDuplicates[T ~string](values []T) []T {
	kept := make([]T, 0, len(values))
	for _, value := range values {
		value = T(strings.TrimSpace(string(value)))
		if !slices.Contains(kept, value) {
			kept = append(kept, value)
		}
	}
	return kept
}

// Validate rend l'erreur de la première règle violée, dans l'ordre où
// l'opérateur remplit le formulaire. d doit être complétée.
func (d ServiceDefinition) Validate() error {
	if d.MachineID == "" {
		return ErrMachineInvalid
	}
	if !namePattern.MatchString(d.Name) {
		return ErrNameInvalid
	}
	if utf8.RuneCountInString(d.Title) > MaxTitleLength {
		return ErrTitleInvalid
	}
	if !slices.Contains(Natures(), d.Nature) {
		return ErrNatureInvalid
	}
	if !slices.Contains(Origins(), d.Origin) {
		return ErrOriginInvalid
	}
	if err := d.validateComposeProject(); err != nil {
		return err
	}
	if err := validatePublicURL(d.PublicURL); err != nil {
		return err
	}
	if err := d.validateProvides(); err != nil {
		return err
	}
	return d.validateUnits()
}

// Le projet Compose est ce qui permet de retrouver le service parmi les
// conteneurs : sans objet pour un conteneur seul ou un binaire.
func (d ServiceDefinition) validateComposeProject() error {
	if d.Origin != OriginCompose {
		if d.ComposeProject != "" {
			return ErrComposeProjectInvalid
		}
		return nil
	}
	if !namePattern.MatchString(d.ComposeProject) {
		return ErrComposeProjectInvalid
	}
	return nil
}

func validatePublicURL(address string) error {
	if address == "" {
		return nil
	}
	if len(address) > MaxPublicURLLength {
		return ErrPublicURLInvalid
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ErrPublicURLInvalid
	}
	return nil
}

func (d ServiceDefinition) validateProvides() error {
	for _, capability := range d.Provides {
		if !slices.Contains(Capabilities(), capability) {
			return ErrProvidesInvalid
		}
	}
	if len(d.Provides) > 0 && d.Nature != NatureInfra {
		return ErrProvidesNeedInfra
	}
	return nil
}

func (d ServiceDefinition) validateUnits() error {
	if len(d.Units) < 1 || len(d.Units) > MaxUnits {
		return ErrUnitsInvalid
	}
	seen := make(map[string]bool, len(d.Units))
	keys := make(map[string]bool, len(d.Units))
	for _, unit := range d.Units {
		if !unitNamePattern.MatchString(unit.Name) {
			return ErrUnitNameInvalid
		}
		if seen[unit.Name] {
			return ErrUnitNameTaken
		}
		seen[unit.Name] = true
		if err := unit.validate(d.Origin); err != nil {
			return err
		}
		for _, key := range []string{"container:" + unit.ContainerName, "systemd:" + unit.SystemdUnit} {
			if strings.HasSuffix(key, ":") {
				continue
			}
			if keys[key] {
				return ErrUnitKeyDuplicate
			}
			keys[key] = true
		}
	}
	return nil
}

func (u UnitDefinition) validate(origin Origin) error {
	if err := validateImage(u.Image, unitKindOf(origin)); err != nil {
		return err
	}
	if err := validateVersion(u.Version); err != nil {
		return err
	}
	if u.Digest != "" && (unitKindOf(origin) == UnitBinary || !digestPattern.MatchString(u.Digest)) {
		return ErrDigestInvalid
	}
	if err := u.validateMatchKeys(origin); err != nil {
		return err
	}
	if err := validatePorts(u.Ports); err != nil {
		return err
	}
	if err := u.validateDataPath(origin); err != nil {
		return err
	}
	return u.validateUpdatePolicy(origin)
}

// Un binaire n'a pas d'image à interroger : sa seule politique est celle par
// défaut.
func (u UnitDefinition) validateUpdatePolicy(origin Origin) error {
	if !container.IsUpdatePolicy(u.UpdatePolicy) {
		return ErrUpdatePolicyInvalid
	}
	if unitKindOf(origin) == UnitBinary && u.UpdatePolicy != container.PolicyWatch {
		return ErrUpdatePolicyInvalid
	}
	return nil
}

// Une image porte son nom, la version et l'empreinte sont à côté : une
// référence qui les mêlerait ne se comparerait plus à ce que Docker montre.
func validateImage(image string, kind UnitKind) error {
	if kind == UnitBinary {
		if image != "" {
			return ErrImageInvalid
		}
		return nil
	}
	if image == "" || len(image) > MaxImageLength || strings.ContainsAny(image, " \t@") {
		return ErrImageInvalid
	}
	// Un « : » avant le dernier « / » est un port de registre, pas une version.
	if strings.Contains(image[strings.LastIndex(image, "/")+1:], ":") {
		return ErrImageInvalid
	}
	// L'écart lit l'image avec registry.Parse : une image qu'il refuse
	// (des majuscules, par exemple) serait « illisible » pour toujours.
	if _, err := registry.Parse(image); err != nil {
		return ErrImageInvalid
	}
	return nil
}

// CheckVersion juge une version qu'un geste exige (passer à un tag) : la
// forme d'un tag, jamais vide, jamais latest. Le déploiement l'appelle avant
// de rendre le compose ; l'annuaire, avant d'offrir et d'écrire.
func CheckVersion(version string) error {
	if version == "" {
		return ErrVersionInvalid
	}
	return validateVersion(version)
}

// « latest » ne dit pas quelle version tourne : la déclaration ne
// permettrait plus de savoir si elle est à jour.
func validateVersion(version string) error {
	if version == "" {
		return nil
	}
	if !versionPattern.MatchString(version) {
		return ErrVersionInvalid
	}
	if strings.EqualFold(version, "latest") {
		return ErrVersionLatest
	}
	return nil
}

// La clé d'observation dépend de l'origine : le nom du conteneur pour un
// conteneur seul, l'unité systemd pour un binaire, rien pour Compose, où
// le nom de l'unité est celui du service dans le projet.
func (u UnitDefinition) validateMatchKeys(origin Origin) error {
	if origin == OriginContainer {
		if !containerNamePattern.MatchString(u.ContainerName) {
			return ErrContainerNameInvalid
		}
	} else if u.ContainerName != "" {
		return ErrContainerNameInvalid
	}
	if origin == OriginBinary {
		if !systemdUnitPattern.MatchString(u.SystemdUnit) {
			return ErrSystemdUnitInvalid
		}
	} else if u.SystemdUnit != "" {
		return ErrSystemdUnitInvalid
	}
	return nil
}

// Le dossier de données est celui du conteneur : un binaire n'en monte pas.
// « . » et « .. » sont refusés par la propreté du chemin, « / » parce que
// monter la racine du conteneur le rendrait inutilisable.
func (u UnitDefinition) validateDataPath(origin Origin) error {
	if u.DataPath == "" {
		return nil
	}
	isClean := dataPathPattern.MatchString(u.DataPath) && path.Clean(u.DataPath) == u.DataPath
	if unitKindOf(origin) == UnitBinary || !isClean {
		return ErrDataPathInvalid
	}
	return nil
}

func validatePorts(ports []string) error {
	if len(ports) > MaxPorts {
		return ErrPortsInvalid
	}
	for _, port := range ports {
		match := portPattern.FindStringSubmatch(port)
		if match == nil {
			return ErrPortsInvalid
		}
		for _, number := range match[1:] {
			if number != "" && !isPortNumber(number) {
				return ErrPortsInvalid
			}
		}
	}
	return nil
}

func isPortNumber(text string) bool {
	number, err := strconv.Atoi(text)
	return err == nil && number >= 1 && number <= 65535
}

func (d ProjectDefinition) Complete() ProjectDefinition {
	d.Name = strings.TrimSpace(d.Name)
	d.Title = strings.TrimSpace(d.Title)
	return d
}

func (d ProjectDefinition) Validate() error {
	if !namePattern.MatchString(d.Name) {
		return ErrProjectNameInvalid
	}
	if utf8.RuneCountInString(d.Title) > MaxTitleLength {
		return ErrProjectTitleInvalid
	}
	return nil
}
