package agentwire

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
)

// Le compose.yml d'un déploiement s'écrit ici, des deux côtés : le serveur le
// rend depuis l'annuaire, l'agent le relit et le rend à nouveau avant de
// l'écrire. Un fichier qui n'est pas, octet pour octet, ce que RenderCompose
// produirait de valeurs valables est refusé : ni privilège, ni réseau de
// l'hôte, ni montage hors de data/<unité>, même si l'ordre est signé.

// ComposeUnit est ce qu'une unité donne au fichier : son nom (celui du service
// Compose), l'image épinglée (image:version, puis @sha256:… s'il y a une
// empreinte), ses ports et, facultatif, le dossier où monter ./data/<nom>.
type ComposeUnit struct {
	Name     string
	Image    string
	Ports    []string
	DataPath string
}

// Les refus de RenderCompose, un par valeur : le serveur les traduit en refus
// de l'annuaire, l'agent en ordre mal formé.
var (
	ErrComposeProject   = errors.New("compose project is not a folder name")
	ErrComposeServiceID = errors.New("service id is not an identifier")
	ErrComposeUnits     = errors.New("compose file holds no unit, or too many")
	ErrComposeUnitName  = errors.New("unit name is not a folder name")
	ErrComposeImage     = errors.New("image is not a pinned reference")
	ErrComposePort      = errors.New("port is not port, host:port, with an optional /tcp or /udp")
	ErrComposeDataPath  = errors.New("data path is not a clean absolute path")
	ErrComposeTooLarge  = errors.New("compose file is larger than an order carries")
	// ErrComposeNotOurs : le texte reçu n'est pas celui que RenderCompose
	// écrirait.
	ErrComposeNotOurs = errors.New("compose file is not the one the server renders")
)

var (
	serviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	// Une référence épinglée : un nom (registre et port compris), un tag, puis
	// l'empreinte s'il y en a une. Ni espace, ni « $ » (Compose interpolerait
	// l'.env dans le nom de l'image), ni « { », « # », guillemet ou retour.
	composeImagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:-]{0,511}:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}(@sha256:[0-9a-f]{64})?$`)
	composePortPattern  = regexp.MustCompile(`^(?:([0-9]{1,5}):)?([0-9]{1,5})(?:/(?:tcp|udp))?$`)
	composeDataPattern  = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,254}$`)
)

const (
	composeRestart  = "unless-stopped"
	composeEnvFile  = ".env"
	composeDataRoot = "data"
	maxComposePorts = 64
)

// IsComposeImage : une référence épinglée que Compose lit telle quelle.
func IsComposeImage(reference string) bool {
	return composeImagePattern.MatchString(reference)
}

// IsComposePort : la forme courte d'un port, nombres entre 1 et 65535.
func IsComposePort(port string) bool {
	match := composePortPattern.FindStringSubmatch(port)
	if match == nil {
		return false
	}
	for _, number := range match[1:] {
		if number == "" {
			continue
		}
		value, err := strconv.Atoi(number)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}
	return true
}

// IsComposeDataPath : un chemin absolu dans le conteneur, déjà propre, jamais
// « / » ; sans « : », il ne peut pas ajouter d'option au montage.
func IsComposeDataPath(target string) bool {
	return composeDataPattern.MatchString(target) && path.Clean(target) == target
}

// La forme du fichier. Rien d'autre ne s'y écrit.
type composeFile struct {
	Name     string      `yaml:"name"`
	Services serviceList `yaml:"services"`
}

type composeService struct {
	Image   string            `yaml:"image"`
	Restart string            `yaml:"restart"`
	Ports   []quotedText      `yaml:"ports,omitempty"`
	Volumes []string          `yaml:"volumes,omitempty"`
	EnvFile []envFile         `yaml:"env_file"`
	Labels  map[string]string `yaml:"labels"`
}

// envFile : Compose 2.24 sait ignorer un fichier absent (required: false).
type envFile struct {
	Path     string `yaml:"path"`
	Required bool   `yaml:"required"`
}

// quotedText s'écrit toujours entre guillemets : « 8080:80 » nu serait lu
// comme un nombre sexagésimal par les lecteurs YAML 1.1.
type quotedText string

func (q quotedText) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Style: yaml.DoubleQuotedStyle, Value: string(q)}, nil
}

type namedService struct {
	name    string
	service composeService
}

// serviceList garde l'ordre des unités : une carte Go s'écrirait triée.
type serviceList []namedService

func (l serviceList) MarshalYAML() (any, error) {
	mapping := &yaml.Node{Kind: yaml.MappingNode}
	for _, item := range l {
		var key, value yaml.Node
		if err := key.Encode(item.name); err != nil {
			return nil, err
		}
		if err := value.Encode(item.service); err != nil {
			return nil, err
		}
		mapping.Content = append(mapping.Content, &key, &value)
	}
	return mapping, nil
}

// RenderCompose écrit le compose.yml d'un projet, et rend les noms des unités
// qui montent un dossier de données. Chaque valeur est jugée avant d'être
// écrite, puis encodée par l'encodeur YAML : aucune ne peut ajouter une clé.
// Mêmes valeurs, même fichier, octet pour octet.
func RenderCompose(project, serviceID string, units []ComposeUnit) (compose []byte, dataDirs []string, err error) {
	if !IsComposeProject(project) {
		return nil, nil, ErrComposeProject
	}
	if !serviceIDPattern.MatchString(serviceID) {
		return nil, nil, ErrComposeServiceID
	}
	if len(units) == 0 || len(units) > MaxDataDirs {
		return nil, nil, ErrComposeUnits
	}
	file := composeFile{Name: project}
	seen := map[string]bool{}
	for _, unit := range units {
		entry, err := composeServiceOf(serviceID, unit)
		if err != nil {
			return nil, nil, err
		}
		if seen[unit.Name] {
			return nil, nil, ErrComposeUnitName
		}
		seen[unit.Name] = true
		if unit.DataPath != "" {
			dataDirs = append(dataDirs, unit.Name)
		}
		file.Services = append(file.Services, namedService{name: unit.Name, service: entry})
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(file); err != nil {
		return nil, nil, fmt.Errorf("encode compose file: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, fmt.Errorf("encode compose file: %w", err)
	}
	if buffer.Len() > MaxComposeBytes {
		return nil, nil, ErrComposeTooLarge
	}
	return buffer.Bytes(), dataDirs, nil
}

func composeServiceOf(serviceID string, unit ComposeUnit) (composeService, error) {
	if !IsDataDirName(unit.Name) {
		return composeService{}, ErrComposeUnitName
	}
	if !IsComposeImage(unit.Image) {
		return composeService{}, ErrComposeImage
	}
	if len(unit.Ports) > maxComposePorts {
		return composeService{}, ErrComposePort
	}
	entry := composeService{
		Image:   unit.Image,
		Restart: composeRestart,
		EnvFile: []envFile{{Path: composeEnvFile, Required: false}},
		Labels: map[string]string{
			brand.LabelPrefix + container.LabelService: serviceID,
			brand.LabelPrefix + container.LabelUnit:    unit.Name,
		},
	}
	for _, port := range unit.Ports {
		if !IsComposePort(port) {
			return composeService{}, ErrComposePort
		}
		entry.Ports = append(entry.Ports, quotedText(port))
	}
	if unit.DataPath != "" {
		if !IsComposeDataPath(unit.DataPath) {
			return composeService{}, ErrComposeDataPath
		}
		entry.Volumes = []string{dataVolumePrefix(unit.Name) + unit.DataPath}
	}
	return entry, nil
}

// dataVolumePrefix : le seul montage qu'un service porte, ./data/<unité>:.
func dataVolumePrefix(name string) string {
	return "./" + composeDataRoot + "/" + name + ":"
}

// CheckCompose dit si un compose.yml reçu dans un ordre est exactement celui
// que RenderCompose écrirait pour ce projet, ce service et ces dossiers de
// données. L'agent le relit pour en tirer les valeurs, les rejuge en le
// rendant à nouveau, puis compare les octets : une ancre, une balise, un
// commentaire, une clé de plus (privileged, network_mode, cap_add, pid,
// volumes vers l'hôte…) ou un « $ » que Compose interpolerait le font refuser.
func CheckCompose(compose, project, serviceID string, dataDirs []string) error {
	if len(compose) > MaxComposeBytes || strings.ContainsRune(compose, '$') {
		return ErrComposeNotOurs
	}
	var read struct {
		Services yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(compose), &read); err != nil || read.Services.Kind != yaml.MappingNode {
		return ErrComposeNotOurs
	}
	pairs := read.Services.Content
	units := make([]ComposeUnit, 0, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		unit, err := composeUnitOf(pairs[index], pairs[index+1])
		if err != nil {
			return err
		}
		units = append(units, unit)
	}
	rendered, renderedDirs, err := RenderCompose(project, serviceID, units)
	if err != nil || string(rendered) != compose || !slices.Equal(renderedDirs, dataDirs) {
		return ErrComposeNotOurs
	}
	return nil
}

// composeUnitOf relit une unité : l'image, les ports, le montage. Le reste
// (restart, env_file, labels) se réécrit à l'identique ou fait échouer la
// comparaison.
func composeUnitOf(key, value *yaml.Node) (ComposeUnit, error) {
	var read struct {
		Image   string   `yaml:"image"`
		Ports   []string `yaml:"ports"`
		Volumes []string `yaml:"volumes"`
	}
	if key.Kind != yaml.ScalarNode || value.Decode(&read) != nil || len(read.Volumes) > 1 {
		return ComposeUnit{}, ErrComposeNotOurs
	}
	unit := ComposeUnit{Name: key.Value, Image: read.Image, Ports: read.Ports}
	if len(read.Volumes) == 1 {
		target, isData := strings.CutPrefix(read.Volumes[0], dataVolumePrefix(unit.Name))
		if !isData || target == "" {
			return ComposeUnit{}, ErrComposeNotOurs
		}
		unit.DataPath = target
	}
	return unit, nil
}
