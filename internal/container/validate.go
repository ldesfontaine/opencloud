package container

import (
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// discard dit un élément écarté d'un rapport, et pourquoi : une ligne de
// journal par élément, pour que l'opérateur sache ce qui manque.
type discard struct {
	what   string
	reason string
}

// invalidError porte le motif d'un élément hors de ce qu'un agent peut
// observer ; errors.Is le rapproche de ErrReportInvalid.
type invalidError struct{ reason string }

func (e invalidError) Error() string { return e.reason }

func (e invalidError) Unwrap() error { return ErrReportInvalid }

func invalid(reason string) error { return invalidError{reason: reason} }

// validate refuse en bloc un rapport dont la forme d'ensemble est absurde
// (trop d'éléments, un moteur absent sans raison) ; le signal de vie compte
// quand même : c'est le serveur qui répond, pas ce package. Un élément
// invalide (un conteneur, un réseau, un événement, une mesure, un
// rattachement) est écarté du rapport rendu et dit dans les écarts : un seul
// fautif ne doit pas faire perdre l'inventaire des autres.
func validate(report Report, now time.Time) (Report, []discard, error) {
	if len(report.Inventory) > MaxContainersPerReport || len(report.Events) > MaxEventsPerReport || len(report.Stats) > MaxStatsPerReport {
		return Report{}, nil, ErrReportInvalid
	}
	if len(report.Networks) > MaxNetworksPerMachine || len(report.Volumes) > MaxVolumesPerMachine {
		return Report{}, nil, ErrReportInvalid
	}
	if report.Engine != nil && !report.Engine.Present && report.Engine.Reason == "" {
		return Report{}, nil, ErrReportInvalid
	}
	var discards []discard
	kept := report
	kept.Networks = nil
	for _, network := range report.Networks {
		if err := validateNetwork(network); err != nil {
			discards = append(discards, discard{"network " + network.Name, err.Error()})
			continue
		}
		kept.Networks = append(kept.Networks, network)
	}
	kept.Volumes = nil
	for _, volume := range report.Volumes {
		if err := validateVolume(volume); err != nil {
			discards = append(discards, discard{"volume " + volume.Name, err.Error()})
			continue
		}
		kept.Volumes = append(kept.Volumes, volume)
	}
	kept.Inventory = nil
	for _, container := range report.Inventory {
		cleaned, dropped, err := validateContainer(container, now)
		discards = append(discards, dropped...)
		if err != nil {
			discards = append(discards, discard{"container " + container.Name, err.Error()})
			continue
		}
		kept.Inventory = append(kept.Inventory, cleaned)
	}
	kept.Events = nil
	for _, event := range report.Events {
		cleaned, dropped, err := validateEvent(event, now)
		discards = append(discards, dropped...)
		if err != nil {
			discards = append(discards, discard{"event " + event.Action, err.Error()})
			continue
		}
		kept.Events = append(kept.Events, cleaned)
	}
	kept.Stats = nil
	for _, stat := range report.Stats {
		if err := validateStat(stat, now); err != nil {
			discards = append(discards, discard{"stat " + stat.ContainerID, err.Error()})
			continue
		}
		kept.Stats = append(kept.Stats, stat)
	}
	return kept, discards, nil
}

func validateVolume(volume VolumeReport) error {
	if volume.Name == "" || len(volume.Name) > MaxNameLength || hasControlOrBadUTF8(volume.Name) {
		return invalid("nom du volume")
	}
	if volume.Driver == "" || len(volume.Driver) > MaxNameLength || hasControlOrBadUTF8(volume.Driver) {
		return invalid("pilote du volume")
	}
	if len(volume.UsedBy) > MaxVolumeUsers {
		return invalid("conteneurs du volume")
	}
	for _, user := range volume.UsedBy {
		if user == "" || len(user) > MaxNameLength || hasControlOrBadUTF8(user) {
			return invalid("nom d'un conteneur du volume")
		}
	}
	return nil
}

func validateNetwork(network NetworkReport) error {
	if !isContainerID(network.NetworkID) || network.Name == "" || len(network.Name) > MaxNameLength {
		return invalid("identifiant ou nom du réseau")
	}
	if network.Driver == "" || len(network.Driver) > MaxNameLength || len(network.Group) > MaxNameLength {
		return invalid("pilote ou groupe du réseau")
	}
	return nil
}

// validateContainer rend le conteneur sans ses rattachements réseau
// invalides (un conteneur tout juste créé n'a pas encore d'identifiant de
// réseau), ou une erreur si c'est le conteneur lui-même qui l'est.
func validateContainer(container Observed, now time.Time) (Observed, []discard, error) {
	var discards []discard
	container.Networks, discards = keepAttachments(container)
	var labelDiscards []discard
	container.Labels, labelDiscards = keepLabels(container)
	discards = append(discards, labelDiscards...)
	return container, discards, checkContainer(container, now)
}

func checkContainer(container Observed, now time.Time) error {
	if !isContainerID(container.ContainerID) || container.Name == "" || len(container.Name) > MaxNameLength {
		return invalid("identifiant, nom ou longueur du nom")
	}
	// Le nom entre dans les messages d'alerte : un saut de ligne y ferait une
	// ligne de plus, signée openCloud. Docker n'en laisse passer aucun.
	if hasControlOrBadUTF8(container.Name) {
		return invalid("caractère de contrôle ou UTF-8 invalide dans le nom")
	}
	if len(container.Group) > MaxNameLength || len(container.Image) > MaxImageLength || len(container.ImageID) > MaxImageLength {
		return invalid("longueur d'un champ de groupe ou d'image")
	}
	if len(container.ComposeService) > MaxNameLength || len(container.ComposeDir) > MaxPathLength || len(container.ComposeFile) > MaxPathLength {
		return invalid("longueur d'un champ Compose")
	}
	if !isState(container.State) || !isHealth(container.Health) || container.RestartCount < 0 {
		return invalid("état ou santé inconnus, ou redémarrages négatifs")
	}
	if len(container.Ports) > MaxPortsPerContainer {
		return invalid("trop de ports")
	}
	for _, port := range container.Ports {
		if port.HostPort <= 0 || port.HostPort > 65535 || port.ContainerPort <= 0 || port.ContainerPort > 65535 || port.Protocol == "" {
			return invalid("port hors limites")
		}
	}
	if container.CreatedAt.IsZero() || isTooLate(container.CreatedAt, now) {
		return invalid("date de création absente ou dans le futur")
	}
	return validateNetworking(container)
}

// keepAttachments écarte les rattachements réseau qu'un agent n'a pas pu
// observer : sans identifiant de réseau (Docker n'en donne pas encore à un
// conteneur tout juste créé), sans nom, ou illisibles.
func keepAttachments(container Observed) ([]Attachment, []discard) {
	var kept []Attachment
	var discards []discard
	for _, attachment := range container.Networks {
		if err := checkAttachment(attachment); err != nil {
			discards = append(discards, discard{"network attachment " + attachment.Name + " of container " + container.Name, err.Error()})
			continue
		}
		kept = append(kept, attachment)
	}
	return kept, discards
}

var (
	// Ce que valent les deux labels qu'openCloud lit : l'identifiant d'un
	// service déclaré (préfixe et 16 hexadécimaux) et le nom d'une unité.
	// Une valeur d'une autre forme ne désigne rien : elle rend le conteneur
	// étiqueté mais orphelin, jamais rattaché par défaut.
	labelServiceValue = regexp.MustCompile(`^svc_[0-9a-f]{16}$`)
	labelUnitValue    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,62}$`)
)

// keepLabels écarte les labels d'un conteneur dont la carte sort des bornes,
// et lui seul : le conteneur reste dans l'inventaire, étiqueté mais mal
// (LabelInvalid), donc orphelin : il ne retombe ni sur Compose ni sur le nom,
// parce qu'un label faux qui se replierait en silence cacherait l'erreur. Un
// conteneur fautif ne doit pas faire perdre les autres, et des labels
// tronqués pourraient désigner autre chose que ce que l'opérateur a écrit.
func keepLabels(container Observed) (map[string]string, []discard) {
	if len(container.Labels) == 0 {
		return nil, nil
	}
	what := "labels of container " + container.Name
	refuse := func(reason string) (map[string]string, []discard) {
		return map[string]string{LabelInvalid: "1"}, []discard{{what, invalid(reason).Error()}}
	}
	if len(container.Labels) > MaxLabelsPerContainer {
		return refuse("trop de labels")
	}
	for key, value := range container.Labels {
		if key == "" || len(key) > MaxLabelKeyLength || len(value) > MaxNameLength || hasControlOrBadUTF8(key) || hasControlOrBadUTF8(value) {
			return refuse("clé ou valeur de label hors limites")
		}
	}
	if value, found := container.Labels[LabelService]; found && !labelServiceValue.MatchString(value) {
		return refuse("identifiant de service illisible")
	}
	if value, found := container.Labels[LabelUnit]; found && !labelUnitValue.MatchString(value) {
		return refuse("nom d'unité illisible")
	}
	return container.Labels, nil
}

func hasControlOrBadUTF8(text string) bool {
	return !utf8.ValidString(text) || strings.ContainsFunc(text, unicode.IsControl)
}

func checkAttachment(attachment Attachment) error {
	if !isContainerID(attachment.NetworkID) || attachment.Name == "" || len(attachment.Name) > MaxNameLength || len(attachment.Aliases) > MaxAliasesPerNetwork {
		return invalid("rattachement réseau sans identifiant valide, sans nom ou avec trop d'alias")
	}
	if attachment.IP != "" {
		if _, err := netip.ParseAddr(attachment.IP); err != nil {
			return invalid("adresse IP du rattachement illisible")
		}
	}
	for _, alias := range attachment.Aliases {
		if alias == "" || len(alias) > MaxNameLength {
			return invalid("alias vide ou trop long")
		}
	}
	return nil
}

func validateNetworking(container Observed) error {
	if len(container.NetworkMode) > MaxNameLength || len(container.Networks) > MaxNetworksPerContainer || len(container.DependsOn) > MaxDependencies {
		return invalid("mode réseau, réseaux ou dépendances en trop")
	}
	for _, dependency := range container.DependsOn {
		if dependency.Name == "" || len(dependency.Name) > MaxNameLength || (dependency.Source != DependencyCompose && dependency.Source != DependencyLink) {
			return invalid("dépendance sans nom ou de source inconnue")
		}
	}
	return nil
}

func validateEvent(event Event, now time.Time) (Event, []discard, error) {
	if !isContainerID(event.ContainerID) || event.Action == "" || event.At.IsZero() || isTooLate(event.At, now) {
		return event, nil, invalid("identifiant, action ou date de l'événement")
	}
	if !isState(event.State) || !isHealth(event.Health) || len(event.Snippet) > MaxSnippetBytes {
		return event, nil, invalid("état, santé ou extrait de l'événement")
	}
	if event.Container != nil {
		if event.Container.ContainerID != event.ContainerID {
			return event, nil, invalid("identifiant du conteneur de l'événement différent")
		}
		cleaned, discards, err := validateContainer(*event.Container, now)
		event.Container = &cleaned
		return event, discards, err
	}
	return event, nil, nil
}

func validateStat(stat Stat, now time.Time) error {
	if !isContainerID(stat.ContainerID) || stat.SampledAt.IsZero() || isTooLate(stat.SampledAt, now) || stat.SampledAt.Before(now.Add(-SampleRetention)) {
		return invalid("identifiant, date ou ancienneté de la mesure")
	}
	if math.IsNaN(stat.CPUPercent) || math.IsInf(stat.CPUPercent, 0) || stat.CPUPercent < 0 || stat.MemUsed < 0 || stat.MemLimit < 0 {
		return invalid("mesure négative ou non finie")
	}
	return nil
}

// Un identifiant Docker, de conteneur comme de réseau : 64 caractères
// hexadécimaux.
func isContainerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// isState accepte l'état vide : un événement qui n'en change pas.
func isState(state State) bool {
	switch state {
	case "", StateCreated, StateRunning, StatePaused, StateRestarting, StateRemoving, StateExited, StateDead:
		return true
	}
	return false
}

func isHealth(health Health) bool {
	return health == "" || slices.Contains(Healths(), health)
}

func isTooLate(at, now time.Time) bool {
	return at.After(now.Add(maxFutureSkew))
}
