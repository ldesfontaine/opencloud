package manifest

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Au-delà, le fichier est tellement faux que le reste n'apprend rien.
const maxProblems = 100

// maxYAMLMessage borne ce que la bibliothèque dit d'une erreur de syntaxe.
const maxYAMLMessage = 200

const (
	probeGrain     = time.Second
	heartbeatGrain = time.Minute
)

// fileTooLargeText est ce que le message cite : la borne, pas la taille.
var fileTooLargeText = strconv.Itoa(MaxFileBytes>>10) + " Kio"

// yamlLine retrouve la ligne dans le message d'une erreur de la bibliothèque.
var yamlLine = regexp.MustCompile(`^yaml: (?:line (\d+): )?`)

// Parse lit un fichier. Il parcourt l'arbre YAML à la main plutôt que de le
// décoder par réflexion : chaque refus porte sa ligne, une clé inconnue est
// refusée et une section pas encore prise en charge aussi. Le fichier rendu
// ne vaut que s'il n'y a aucun refus.
func Parse(data []byte) (File, []Problem) {
	if len(data) > MaxFileBytes {
		return File{}, []Problem{{Key: KeyFileTooLarge, Value: fileTooLargeText}}
	}
	root, problem := decodeSingleDocument(data)
	if problem != nil {
		return File{}, []Problem{*problem}
	}
	read := &fileReader{}
	read.refuseAliases(root)
	if len(read.problems) > 0 {
		return File{}, read.problems
	}
	file := read.readRoot(root)
	if len(read.problems) > 0 {
		return File{}, read.problems
	}
	return file, nil
}

// decodeSingleDocument refuse un fichier vide de sens, illisible, ou qui
// porte plusieurs documents : un seul document est un annuaire.
func decodeSingleDocument(data []byte) (*yaml.Node, *Problem) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	err := decoder.Decode(&document)
	if errors.Is(err, io.EOF) {
		return nil, &Problem{Key: KeyVersionMissing, Line: 1, Column: 1}
	}
	if err != nil {
		return nil, yamlProblem(err, data)
	}
	var second yaml.Node
	err = decoder.Decode(&second)
	if err == nil {
		// La ligne du contenu du deuxième document, pas celle de son « --- ».
		where := &second
		if len(second.Content) > 0 {
			where = second.Content[0]
		}
		return nil, &Problem{Key: KeyDocumentMultiple, Line: where.Line, Column: where.Column}
	}
	if !errors.Is(err, io.EOF) {
		return nil, yamlProblem(err, data)
	}
	if len(document.Content) == 0 {
		return nil, &Problem{Key: KeyVersionMissing, Line: 1, Column: 1}
	}
	return document.Content[0], nil
}

// yamlProblem traduit l'erreur de la bibliothèque. Elle ne donne qu'une ligne,
// jamais de colonne : Column reste à 0. Le message est borné et ne recopie pas
// ce que le fichier contient (le nom d'une ancre inconnue).
func yamlProblem(err error, data []byte) *Problem {
	message := err.Error()
	line := 1
	if match := yamlLine.FindStringSubmatch(message); match != nil {
		if match[1] != "" {
			line, _ = strconv.Atoi(match[1])
		}
		message = strings.TrimPrefix(message, match[0])
	}
	if name, found := strings.CutPrefix(message, "unknown anchor '"); found {
		// La bibliothèque ne donne pas de ligne pour celle-là : on la cherche,
		// sans recopier le nom dans le message.
		if end := strings.Index(name, "'"); end >= 0 {
			if at := bytes.Index(data, []byte("*"+name[:end])); at >= 0 {
				line = 1 + bytes.Count(data[:at], []byte("\n"))
			}
		}
		message = "unknown anchor"
	}
	if len(message) > maxYAMLMessage {
		message = message[:maxYAMLMessage]
	}
	return &Problem{Key: KeyYAMLInvalid, Line: line, Value: message}
}

type fileReader struct {
	problems []Problem
}

func (r *fileReader) add(key string, node *yaml.Node, value string) {
	if len(r.problems) >= maxProblems {
		return
	}
	r.problems = append(r.problems, Problem{Key: key, Line: node.Line, Column: node.Column, Value: value})
}

// refuseAliases refuse les ancres et les alias : ils feraient lire autre
// chose que ce qui est écrit, et un alias récursif gonfle sans fin.
func (r *fileReader) refuseAliases(node *yaml.Node) {
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		r.add(KeyAliasNotAllowed, node, "")
		return
	}
	for _, child := range node.Content {
		r.refuseAliases(child)
	}
}

// --- Les formes ---

// fields lit un bloc de clés. Une clé inconnue, en double ou réservée à une
// section pas encore prise en charge est refusée avec sa ligne ; les autres
// vont à handle avec leur valeur.
func (r *fileReader) fields(node *yaml.Node, name string, known, unsupported []string, handle func(key string, value *yaml.Node)) {
	// Une étiquette personnalisée (!custom {…}) ferait lire autre chose que
	// ce qui est écrit : refusée sur un bloc comme sur un scalaire.
	if node.Kind != yaml.MappingNode || node.ShortTag() != "!!map" {
		r.add(KeyTypeInvalid, node, name)
		return
	}
	seen := map[string]bool{}
	for index := 0; index+1 < len(node.Content); index += 2 {
		keyNode, value := node.Content[index], node.Content[index+1]
		key := keyNode.Value
		if keyNode.Kind != yaml.ScalarNode {
			r.add(KeyTypeInvalid, keyNode, name)
			continue
		}
		switch {
		case seen[key]:
			r.add(KeyDuplicateKey, keyNode, key)
		case slices.Contains(unsupported, key):
			r.add(KeySectionUnsupported, keyNode, key)
		case !slices.Contains(known, key):
			r.add(KeyUnknownKey, keyNode, key)
		default:
			handle(key, value)
		}
		seen[key] = true
	}
}

// entries rend les éléments d'une liste de blocs. Une clé laissée vide
// (« projects: ») est une liste vide.
func (r *fileReader) entries(node *yaml.Node, name string) []*yaml.Node {
	if isNull(node) {
		return nil
	}
	if node.Kind != yaml.SequenceNode || node.ShortTag() != "!!seq" {
		r.add(KeyTypeInvalid, node, name)
		return nil
	}
	return node.Content
}

func isNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.ShortTag() == "!!null"
}

// text lit une chaîne. Un nombre ou un booléen non guillemeté est refusé :
// « 16.4 » et « true » ne sont pas du texte tant qu'on ne les guillemette pas,
// et l'export les guillemette.
func (r *fileReader) text(node *yaml.Node, name string) string {
	if isNull(node) {
		return ""
	}
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!str" {
		r.add(KeyTypeInvalid, node, name)
		return ""
	}
	return node.Value
}

func (r *fileReader) texts(node *yaml.Node, name string) []string {
	var values []string
	for _, item := range r.entries(node, name) {
		values = append(values, r.text(item, name))
	}
	return values
}

func (r *fileReader) integer(node *yaml.Node, name string) int {
	if isNull(node) {
		return 0
	}
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!int" {
		r.add(KeyTypeInvalid, node, name)
		return 0
	}
	number, err := strconv.Atoi(node.Value)
	if err != nil {
		r.add(KeyTypeInvalid, node, name)
	}
	return number
}

func (r *fileReader) boolean(node *yaml.Node, name string) bool {
	if isNull(node) {
		return false
	}
	// Seules « true » et « false » : la bibliothèque lit aussi « True » et
	// « TRUE » comme des booléens, et un « False » pris pour autre chose que
	// ce qu'il dit se tairait.
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!bool" || (node.Value != "true" && node.Value != "false") {
		r.add(KeyTypeInvalid, node, name)
		return false
	}
	return node.Value == "true"
}

// duration lit « 90s », « 1m », « 1h30m » : une unité est obligatoire, et la
// durée tombe juste sur le grain de l'objet (secondes d'une sonde, minutes
// d'une tâche), comme dans l'interface.
func (r *fileReader) duration(node *yaml.Node, name string, grain time.Duration) time.Duration {
	if isNull(node) {
		return 0
	}
	// Un nombre nu reste une durée sans unité, avec son message ; toute autre
	// forme (étiquette personnalisée comprise) n'est pas une durée.
	if node.Kind != yaml.ScalarNode || !slices.Contains([]string{"!!str", "!!int", "!!float"}, node.ShortTag()) {
		r.add(KeyTypeInvalid, node, name)
		return 0
	}
	parsed, ok := parseDuration(node.Value, grain)
	if !ok {
		r.add(KeyDurationInvalid, node, node.Value)
	}
	return parsed
}

// refuseZero refuse un zéro écrit : le domaine lit zéro comme « absent » et
// poserait le défaut, là où l'opérateur a écrit autre chose. Une clé absente,
// elle, prend le défaut sans ambiguïté. Une valeur déjà refusée (before
// compte les refus d'avant sa lecture) ne l'est pas deux fois.
func (r *fileReader) refuseZero(node *yaml.Node, name string, before int, value int64) {
	if len(r.problems) == before && value == 0 && !isNull(node) {
		r.add(KeyZeroValue, node, name)
	}
}

func parseDuration(text string, grain time.Duration) (time.Duration, bool) {
	if text == "" || strings.ContainsAny(text[len(text)-1:], "0123456789.") {
		return 0, false
	}
	parsed, err := time.ParseDuration(text)
	if err != nil || parsed < 0 || parsed%grain != 0 {
		return 0, false
	}
	return parsed, true
}

// --- Le fichier ---

func (r *fileReader) readRoot(root *yaml.Node) File {
	var file File
	hasVersion := false
	known := []string{"version", "machines", "projects", "services", "probes", "heartbeats", "ignored"}
	r.fields(root, "", known, unsupportedRoot, func(key string, value *yaml.Node) {
		switch key {
		case "version":
			hasVersion = true
			r.readVersion(value)
		case "machines":
			file.Machines = r.readMachines(value)
		case "projects":
			file.Projects = r.readProjects(value)
		case "services":
			file.Services = r.readServices(value)
		case "probes":
			file.Probes = r.readProbes(value)
		case "heartbeats":
			file.Heartbeats = r.readHeartbeats(value)
		case "ignored":
			file.Ignored = r.readIgnored(value)
		}
	})
	if !hasVersion && root.Kind == yaml.MappingNode {
		r.add(KeyVersionMissing, root, "")
	}
	return file
}

func (r *fileReader) readVersion(node *yaml.Node) {
	if node.Kind != yaml.ScalarNode || node.ShortTag() != "!!int" {
		r.add(KeyTypeInvalid, node, "version")
		return
	}
	if node.Value != strconv.Itoa(Version) {
		r.add(KeyVersionUnsupported, node, node.Value)
	}
}

func (r *fileReader) readMachines(node *yaml.Node) []Machine {
	var machines []Machine
	for _, entry := range r.entries(node, "machines") {
		machine := Machine{Line: entry.Line}
		r.fields(entry, "machines", []string{"name"}, nil, func(key string, value *yaml.Node) {
			machine.Name = r.text(value, key)
		})
		machines = append(machines, machine)
	}
	return machines
}

func (r *fileReader) readProjects(node *yaml.Node) []Project {
	var projects []Project
	for _, entry := range r.entries(node, "projects") {
		project := Project{Line: entry.Line}
		r.fields(entry, "projects", []string{"id", "name", "title"}, nil, func(key string, value *yaml.Node) {
			switch key {
			case "id":
				project.ID = r.text(value, key)
			case "name":
				project.Name = r.text(value, key)
			case "title":
				project.Title = r.text(value, key)
			}
		})
		projects = append(projects, project)
	}
	return projects
}

func (r *fileReader) readServices(node *yaml.Node) []Service {
	var services []Service
	known := []string{"id", "machine", "name", "title", "nature", "origin", "compose_project", "public_url", "provides", "projects", "units"}
	for _, entry := range r.entries(node, "services") {
		service := Service{Line: entry.Line}
		r.fields(entry, "services", known, unsupportedService, func(key string, value *yaml.Node) {
			switch key {
			case "id":
				service.ID = r.text(value, key)
			case "machine":
				service.Machine = r.text(value, key)
			case "name":
				service.Name = r.text(value, key)
			case "title":
				service.Title = r.text(value, key)
			case "nature":
				service.Nature = directory.Nature(r.text(value, key))
			case "origin":
				service.Origin = directory.Origin(r.text(value, key))
			case "compose_project":
				service.ComposeProject = r.text(value, key)
			case "public_url":
				service.PublicURL = r.text(value, key)
			case "provides":
				for _, capability := range r.texts(value, key) {
					service.Provides = append(service.Provides, directory.Capability(capability))
				}
			case "projects":
				service.Projects = r.texts(value, key)
			case "units":
				service.Units = r.readUnits(value)
			}
		})
		services = append(services, service)
	}
	return services
}

func (r *fileReader) readUnits(node *yaml.Node) []Unit {
	var units []Unit
	known := []string{"name", "image", "version", "digest", "container_name", "systemd_unit", "ports", "data_path", "updates"}
	for _, entry := range r.entries(node, "units") {
		unit := Unit{Line: entry.Line}
		r.fields(entry, "units", known, nil, func(key string, value *yaml.Node) {
			switch key {
			case "name":
				unit.Name = r.text(value, key)
			case "image":
				unit.Image = r.text(value, key)
			case "version":
				unit.Version = r.text(value, key)
			case "digest":
				unit.Digest = r.text(value, key)
			case "container_name":
				unit.ContainerName = r.text(value, key)
			case "systemd_unit":
				unit.SystemdUnit = r.text(value, key)
			case "ports":
				unit.Ports = r.texts(value, key)
			case "data_path":
				unit.DataPath = r.text(value, key)
			case "updates":
				unit.Updates = container.UpdatePolicy(r.text(value, key))
			}
		})
		units = append(units, unit)
	}
	return units
}

func (r *fileReader) readProbes(node *yaml.Node) []Probe {
	var probes []Probe
	known := []string{
		"id", "name", "runner", "service", "kind", "target", "interval", "timeout", "failure_threshold",
		"recovery_threshold", "method", "expected_status", "expected_body", "follow_redirects", "tls",
	}
	for _, entry := range r.entries(node, "probes") {
		item := Probe{Line: entry.Line}
		r.fields(entry, "probes", known, nil, func(key string, value *yaml.Node) {
			switch key {
			case "id":
				item.ID = r.text(value, key)
			case "name":
				item.Name = r.text(value, key)
			case "runner":
				item.Runner = r.text(value, key)
			case "service":
				item.Service = r.text(value, key)
			case "kind":
				item.Kind = probe.Kind(r.text(value, key))
			case "target":
				item.Target = r.text(value, key)
			case "interval":
				before := len(r.problems)
				item.Interval = r.duration(value, key, probeGrain)
				r.refuseZero(value, key, before, int64(item.Interval))
			case "timeout":
				before := len(r.problems)
				item.Timeout = r.duration(value, key, probeGrain)
				r.refuseZero(value, key, before, int64(item.Timeout))
			case "failure_threshold":
				before := len(r.problems)
				item.FailureThreshold = r.integer(value, key)
				r.refuseZero(value, key, before, int64(item.FailureThreshold))
			case "recovery_threshold":
				before := len(r.problems)
				item.RecoveryThreshold = r.integer(value, key)
				r.refuseZero(value, key, before, int64(item.RecoveryThreshold))
			case "method":
				item.Method = r.text(value, key)
			case "expected_status":
				item.ExpectedStatus = r.text(value, key)
			case "expected_body":
				item.ExpectedBody = r.text(value, key)
			case "follow_redirects":
				item.FollowRedirects = r.boolean(value, key)
			case "tls":
				item.TLS = r.boolean(value, key)
			}
		})
		probes = append(probes, item)
	}
	return probes
}

func (r *fileReader) readHeartbeats(node *yaml.Node) []Heartbeat {
	var heartbeats []Heartbeat
	known := []string{"id", "name", "machine", "every", "grace", "token"}
	for _, entry := range r.entries(node, "heartbeats") {
		item := Heartbeat{Line: entry.Line}
		r.fields(entry, "heartbeats", known, unsupportedHeartbeat, func(key string, value *yaml.Node) {
			switch key {
			case "id":
				item.ID = r.text(value, key)
			case "name":
				item.Name = r.text(value, key)
			case "machine":
				item.Machine = r.text(value, key)
			case "every":
				item.Every = r.duration(value, key, heartbeatGrain)
			case "grace":
				item.Grace = r.duration(value, key, heartbeatGrain)
			case "token":
				r.checkSecret(value)
			}
		})
		heartbeats = append(heartbeats, item)
	}
	return heartbeats
}

// checkSecret n'admet que « secret: kept ». Un scalaire est un jeton en
// clair : le fichier va dans Git, c'est une erreur et non un avertissement.
func (r *fileReader) checkSecret(node *yaml.Node) {
	if node.Kind == yaml.ScalarNode && !isNull(node) {
		r.add(KeySecretInFile, node, "")
		return
	}
	kept := false
	if node.Kind == yaml.MappingNode && node.ShortTag() == "!!map" && len(node.Content) == 2 {
		key, value := node.Content[0], node.Content[1]
		kept = key.Kind == yaml.ScalarNode && key.ShortTag() == "!!str" && key.Value == "secret" &&
			value.Kind == yaml.ScalarNode && value.ShortTag() == "!!str" && value.Value == SecretKept
	}
	if !kept {
		r.add(KeySecretInvalid, node, "")
	}
}

func (r *fileReader) readIgnored(node *yaml.Node) []IgnoredKey {
	var keys []IgnoredKey
	for _, entry := range r.entries(node, "ignored") {
		var machine, composeProject, containerName string
		r.fields(entry, "ignored", []string{"machine", "compose_project", "container_name"}, nil, func(key string, value *yaml.Node) {
			switch key {
			case "machine":
				machine = r.text(value, key)
			case "compose_project":
				composeProject = r.text(value, key)
			case "container_name":
				containerName = r.text(value, key)
			}
		})
		key := IgnoredKey{Machine: machine, Line: entry.Line}
		switch {
		case entry.Kind != yaml.MappingNode:
			// Déjà refusé par fields.
		case machine == "":
			// Une clé ignorée est celle d'une machine : sans elle, rien à ignorer.
			r.add(KeyIgnoredInvalid, entry, "")
		case composeProject != "" && containerName == "":
			key.Kind, key.Value = directory.IgnoreComposeProject, composeProject
		case containerName != "" && composeProject == "":
			key.Kind, key.Value = directory.IgnoreContainerName, containerName
		default:
			r.add(KeyIgnoredInvalid, entry, "")
		}
		keys = append(keys, key)
	}
	return keys
}
