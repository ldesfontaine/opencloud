package dockerwatch

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// toContainer fait le conteneur observé de ce que Docker a inspecté.
func toContainer(inspected dockerapi.Container) container.Observed {
	observed := container.Observed{
		ContainerID:  inspected.ID,
		Name:         inspected.ShortName(),
		Group:        inspected.Config.Labels[dockerapi.LabelComposeProject],
		Image:        inspected.Config.Image,
		ImageID:      inspected.Image,
		State:        container.State(inspected.State.Status),
		ExitCode:     inspected.State.ExitCode,
		OOMKilled:    inspected.State.OOMKilled,
		RestartCount: inspected.RestartCount,
		Ports:        publishedPorts(inspected.Network.Ports),
		NetworkMode:  inspected.Host.NetworkMode,
		Privileged:   inspected.Host.Privileged,
		Networks:     attachments(inspected),
		DependsOn:    dependencies(inspected),
		Labels:       ownLabels(inspected.Config.Labels),
		CreatedAt:    dockerapi.ParseTime(inspected.Created),
	}
	if composeService := inspected.Config.Labels[dockerapi.LabelComposeService]; composeService != "" {
		observed.Name = composeService
		observed.ComposeService = composeService
		observed.ComposeDir = inspected.Config.Labels[dockerapi.LabelComposeWorkingDir]
		observed.ComposeFile, _, _ = strings.Cut(inspected.Config.Labels[dockerapi.LabelComposeConfigFiles], ",")
	}
	if inspected.State.Health != nil {
		observed.Health = container.Health(inspected.State.Health.Status)
	}
	if started := dockerapi.ParseTime(inspected.State.StartedAt); !started.IsZero() {
		observed.StartedAt = &started
	}
	if finished := dockerapi.ParseTime(inspected.State.FinishedAt); !finished.IsZero() {
		observed.FinishedAt = &finished
	}
	return observed
}

// labelSuffix est ce qu'un suffixe de label peut être : le même alphabet que
// le nom d'une unité, en minuscules.
var labelSuffix = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,62}$`)

// ownLabels ne garde que nos labels (ceux du préfixe de la marque), préfixe
// retiré : seuls nos labels partent, jamais un label Traefik ou Compose de
// plus. Docker fusionne dans Config.Labels ceux de l'image, que son auteur
// a écrits ; l'identifiant du service, tiré au hasard, les rend inoffensifs.
// Un suffixe ou une valeur hors bornes est écarté, et la carte est coupée à
// sa taille maximale : les deux labels qu'openCloud lit passent d'abord, pour
// qu'une image bavarde ne les chasse pas.
//
// Le préfixe se reconnaît sans tenir compte de la casse : « Opencloud.service »
// est une tentative d'étiquette, pas un label d'autrui. Si des labels ont dû
// partir et qu'il ne reste pas la paire service et unité, la carte porte la
// clé réservée LabelInvalid (le nombre de clés écartées) : le conteneur est étiqueté, mal,
// et le serveur ne le laissera pas retomber sur Compose ou sur son nom.
func ownLabels(labels map[string]string) map[string]string {
	suffixes := make([]string, 0, len(labels))
	discarded := 0
	for key, value := range labels {
		suffix, ours := cutLabelPrefix(key)
		if !ours {
			continue
		}
		if suffix == container.LabelInvalid || !labelSuffix.MatchString(suffix) || value == "" || len(value) > container.MaxNameLength || hasControl(value) || key != brand.LabelPrefix+suffix {
			discarded++
			continue
		}
		suffixes = append(suffixes, suffix)
	}
	sort.Slice(suffixes, func(i, j int) bool {
		first, second := isReadLabel(suffixes[i]), isReadLabel(suffixes[j])
		if first != second {
			return first
		}
		return suffixes[i] < suffixes[j]
	})
	// Les deux labels lus passent d'abord (tri ci-dessus), donc ce qu'on garde
	// d'eux ne dépend pas de la coupe. Le signe d'un label mal formé tient la
	// place d'une clé.
	flagged := discarded > 0 && !(slices.Contains(suffixes, container.LabelService) && slices.Contains(suffixes, container.LabelUnit))
	room := container.MaxLabelsPerContainer
	if flagged {
		room--
	}
	if len(suffixes) > room {
		suffixes = suffixes[:room]
	}
	own := make(map[string]string, len(suffixes)+1)
	for _, suffix := range suffixes {
		own[suffix] = labels[brand.LabelPrefix+suffix]
	}
	if flagged {
		own[container.LabelInvalid] = strconv.Itoa(discarded)
	}
	if len(own) == 0 {
		return nil
	}
	return own
}

// cutLabelPrefix retire le préfixe de la marque, majuscules comprises.
func cutLabelPrefix(key string) (string, bool) {
	if len(key) < len(brand.LabelPrefix) || !strings.EqualFold(key[:len(brand.LabelPrefix)], brand.LabelPrefix) {
		return "", false
	}
	return key[len(brand.LabelPrefix):], true
}

func hasControl(text string) bool {
	return !utf8.ValidString(text) || strings.ContainsFunc(text, unicode.IsControl)
}

func isReadLabel(suffix string) bool {
	return suffix == container.LabelService || suffix == container.LabelUnit
}

// publishedPorts lit « 80/tcp » → [{127.0.0.1 18080}] ; un port exposé
// sans publication n'a pas de liaison et ne compte pas.
func publishedPorts(ports map[string][]dockerapi.PortBinding) []container.Port {
	var published []container.Port
	for key, bindings := range ports {
		containerPort, protocol := splitPortKey(key)
		for _, binding := range bindings {
			hostPort := atoi(binding.HostPort)
			if containerPort == 0 || hostPort == 0 {
				continue
			}
			published = append(published, container.Port{IP: binding.HostIP, HostPort: hostPort, ContainerPort: containerPort, Protocol: protocol})
		}
	}
	sortPorts(published)
	if len(published) > container.MaxPortsPerContainer {
		published = published[:container.MaxPortsPerContainer]
	}
	return published
}

// attachments lit les réseaux joints, par nom ; l'alias qu'est l'id court
// du conteneur n'apprend rien et n'est pas gardé.
func attachments(inspected dockerapi.Container) []container.Attachment {
	shortID := inspected.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	var joined []container.Attachment
	for name, endpoint := range inspected.Network.Networks {
		// Un conteneur tout juste créé n'a pas encore d'identifiant de réseau :
		// ce rattachement-là n'est pas encore un fait, le prochain inventaire
		// le donnera.
		if endpoint.NetworkID == "" {
			continue
		}
		names := endpoint.DNSNames
		if len(names) == 0 {
			names = endpoint.Aliases
		}
		aliases := make([]string, 0, len(names))
		for _, alias := range names {
			if alias != "" && alias != shortID && !contains(aliases, alias) {
				aliases = append(aliases, alias)
			}
		}
		if len(aliases) > container.MaxAliasesPerNetwork {
			aliases = aliases[:container.MaxAliasesPerNetwork]
		}
		joined = append(joined, container.Attachment{NetworkID: endpoint.NetworkID, Name: name, IP: endpoint.IPAddress, Aliases: aliases})
	}
	sort.Slice(joined, func(i, j int) bool { return joined[i].Name < joined[j].Name })
	if len(joined) > container.MaxNetworksPerContainer {
		joined = joined[:container.MaxNetworksPerContainer]
	}
	return joined
}

// dependencies lit les dépendances déclarées : le label Compose
// « db:service_started:false,cache:service_healthy:true » donne des noms de
// services ; un lien « /db:/web/db » donne un nom de conteneur.
func dependencies(inspected dockerapi.Container) []container.Dependency {
	var declared []container.Dependency
	for _, entry := range strings.Split(inspected.Config.Labels[dockerapi.LabelComposeDependsOn], ",") {
		name, _, _ := strings.Cut(entry, ":")
		if name != "" {
			declared = append(declared, container.Dependency{Name: name, Source: container.DependencyCompose})
		}
	}
	for _, link := range inspected.Host.Links {
		target, _, _ := strings.Cut(link, ":")
		target = strings.TrimPrefix(target, "/")
		if target != "" {
			declared = append(declared, container.Dependency{Name: target, Source: container.DependencyLink})
		}
	}
	if len(declared) > container.MaxDependencies {
		declared = declared[:container.MaxDependencies]
	}
	return declared
}

func contains(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}

func splitPortKey(key string) (int, string) {
	port, protocol, ok := strings.Cut(key, "/")
	if !ok {
		return atoi(port), "tcp"
	}
	return atoi(port), protocol
}

func atoi(value string) int {
	number := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		number = number*10 + int(r-'0')
	}
	return number
}

func sortPorts(ports []container.Port) {
	for i := 1; i < len(ports); i++ {
		for j := i; j > 0 && lessPort(ports[j], ports[j-1]); j-- {
			ports[j], ports[j-1] = ports[j-1], ports[j]
		}
	}
}

func lessPort(a, b container.Port) bool {
	if a.HostPort != b.HostPort {
		return a.HostPort < b.HostPort
	}
	return a.Protocol+a.IP < b.Protocol+b.IP
}
