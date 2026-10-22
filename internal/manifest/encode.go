package manifest

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Annotations sont les commentaires de l'export, dans la langue de
// l'interface. L'appelant les tire du catalogue : le paquet n'en importe pas.
type Annotations struct {
	// En tête du fichier ; une ligne par ligne du texte, sans le « # ».
	Header string
	// Au-dessus d'un service que l'export propose, et d'une unité qu'il
	// ajoute à un service déclaré : ils viennent de conteneurs non déclarés,
	// l'opérateur les relit avant d'appliquer.
	Proposed     string
	ProposedUnit string
}

// Encode écrit le fichier, dans l'ordre des listes de file. Les mêmes
// entrées donnent les mêmes octets : ni date, ni ordre de table.
func Encode(file File, annotations Annotations) ([]byte, error) {
	root := block()
	root.HeadComment = annotations.Header
	root.add("version", number(Version))
	root.add("machines", sequence(machineNodes(file.Machines)))
	root.add("projects", sequence(projectNodes(file.Projects)))
	root.add("services", sequence(serviceNodes(file.Services, annotations)))
	root.add("probes", sequence(probeNodes(file.Probes)))
	root.add("heartbeats", sequence(heartbeatNodes(file.Heartbeats)))
	root.add("ignored", sequence(ignoredNodes(file.Ignored)))

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root.Node}}); err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	return output.Bytes(), nil
}

// --- Les noeuds ---

// mapping range les clés d'un bloc dans l'ordre où on les ajoute : c'est ce
// qui rend l'écriture déterministe.
type mapping struct {
	*yaml.Node
}

func block() mapping {
	return mapping{&yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}}
}

func (m mapping) add(key string, value *yaml.Node) {
	m.Content = append(m.Content, text(key), value)
}

func (m mapping) addText(key, value string) {
	if value != "" {
		m.add(key, text(value))
	}
}

// text est toujours une chaîne : l'encodeur guillemette ce qui ne resterait
// pas une chaîne une fois relu (« 16.4 », « true », « 1 »). Il ne connaît pas
// les pièges de YAML 1.1 ni la clé de fusion, que d'autres lecteurs prennent
// pour un booléen, un nombre ou une fusion : on les guillemette ici.
func text(value string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	if value == "<<" || isOldBool(value) || isBase60Float(value) {
		node.Style = yaml.DoubleQuotedStyle
	}
	return node
}

// isOldBool et isBase60Float sont recopiées de go.yaml.in/yaml/v3 (encode.go,
// fonctions du même nom), qui s'en sert pour guillemeter une chaîne quand on
// lui donne une valeur Go mais pas quand on lui donne un yaml.Node. À revoir
// à chaque montée de la bibliothèque.
func isOldBool(s string) bool {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON",
		"n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return true
	}
	return false
}

// De http://yaml.org/type/float.html, comme dans la bibliothèque.
var base60float = regexp.MustCompile(`^[-+]?[0-9][0-9_]*(?::[0-5]?[0-9])+(?:\.[0-9_]*)?$`)

func isBase60Float(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	if !(c == '+' || c == '-' || c >= '0' && c <= '9') || strings.IndexByte(s, ':') < 0 {
		return false
	}
	return base60float.MatchString(s)
}

// quoted force les guillemets : une version ou un port a l'air d'un nombre,
// et celui qui édite « 3.1.2 » en « 3.2 » en ferait un décimal.
func quoted(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

func number(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(value)}
}

func yamlBool(value bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)}
}

func sequence(items []*yaml.Node) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: items}
	if len(items) == 0 {
		node.Style = yaml.FlowStyle
	}
	return node
}

// shortList écrit une liste courte sur une ligne : [proxy, tls].
func shortList(values []string, scalar func(string) *yaml.Node) *yaml.Node {
	items := make([]*yaml.Node, 0, len(values))
	for _, value := range values {
		items = append(items, scalar(value))
	}
	node := sequence(items)
	node.Style = yaml.FlowStyle
	return node
}

// formatDuration écrit une seule unité, la plus grande qui tombe juste :
// 1m, 24h, 90s.
func formatDuration(value time.Duration) string {
	switch {
	case value == 0:
		return "0s"
	case value%time.Hour == 0:
		return strconv.FormatInt(int64(value/time.Hour), 10) + "h"
	case value%time.Minute == 0:
		return strconv.FormatInt(int64(value/time.Minute), 10) + "m"
	case value%time.Second == 0:
		return strconv.FormatInt(int64(value/time.Second), 10) + "s"
	}
	return value.String()
}

func machineNodes(machines []Machine) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(machines))
	for _, item := range machines {
		entry := block()
		entry.add("name", text(item.Name))
		nodes = append(nodes, entry.Node)
	}
	return nodes
}

func projectNodes(projects []Project) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(projects))
	for _, item := range projects {
		entry := block()
		entry.addText("id", item.ID)
		entry.add("name", text(item.Name))
		entry.addText("title", item.Title)
		nodes = append(nodes, entry.Node)
	}
	return nodes
}

func serviceNodes(services []Service, annotations Annotations) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(services))
	for _, item := range services {
		entry := block()
		if item.Proposed {
			entry.HeadComment = annotations.Proposed
		}
		entry.addText("id", item.ID)
		entry.add("machine", text(item.Machine))
		entry.add("name", text(item.Name))
		entry.addText("title", item.Title)
		entry.add("nature", text(string(item.Nature)))
		entry.add("origin", text(string(item.Origin)))
		entry.addText("compose_project", item.ComposeProject)
		entry.addText("public_url", item.PublicURL)
		if len(item.Provides) > 0 {
			provides := make([]string, 0, len(item.Provides))
			for _, capability := range item.Provides {
				provides = append(provides, string(capability))
			}
			entry.add("provides", shortList(provides, text))
		}
		if len(item.Projects) > 0 {
			entry.add("projects", shortList(item.Projects, text))
		}
		if len(item.Units) > 0 {
			entry.add("units", sequence(unitNodes(item.Units, annotations)))
		}
		nodes = append(nodes, entry.Node)
	}
	return nodes
}

func unitNodes(units []Unit, annotations Annotations) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(units))
	for _, item := range units {
		entry := block()
		if item.Proposed {
			entry.HeadComment = annotations.ProposedUnit
		}
		entry.add("name", text(item.Name))
		entry.addText("image", item.Image)
		if item.Version != "" {
			entry.add("version", quoted(item.Version))
		}
		entry.addText("digest", item.Digest)
		entry.addText("container_name", item.ContainerName)
		entry.addText("systemd_unit", item.SystemdUnit)
		if len(item.Ports) > 0 {
			entry.add("ports", shortList(item.Ports, quoted))
		}
		entry.addText("data_path", item.DataPath)
		entry.addText("updates", string(item.Updates))
		nodes = append(nodes, entry.Node)
	}
	return nodes
}

// probeNodes écrit tout champ dont le défaut n'est pas vide, défauts
// compris : un défaut qui change dans le code ne change pas un fichier
// versionné. Les champs HTTP ne vont qu'avec http, « tls » qu'avec tcp.
func probeNodes(probes []Probe) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(probes))
	for _, item := range probes {
		entry := block()
		entry.addText("id", item.ID)
		entry.add("name", text(item.Name))
		entry.addText("runner", item.Runner)
		entry.addText("service", item.Service)
		entry.add("kind", text(string(item.Kind)))
		entry.add("target", text(item.Target))
		entry.add("interval", text(formatDuration(item.Interval)))
		entry.add("timeout", text(formatDuration(item.Timeout)))
		entry.add("failure_threshold", number(item.FailureThreshold))
		entry.add("recovery_threshold", number(item.RecoveryThreshold))
		if item.Kind == probe.KindHTTP {
			entry.addText("method", item.Method)
			entry.addText("expected_status", item.ExpectedStatus)
			entry.addText("expected_body", item.ExpectedBody)
			if item.FollowRedirects {
				entry.add("follow_redirects", yamlBool(true))
			}
		}
		if item.Kind == probe.KindTCP && item.TLS {
			entry.add("tls", yamlBool(true))
		}
		nodes = append(nodes, entry.Node)
	}
	return nodes
}

// heartbeatNodes ne porte jamais le jeton : la tâche n'a que le marqueur.
func heartbeatNodes(heartbeats []Heartbeat) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(heartbeats))
	for _, item := range heartbeats {
		entry := block()
		entry.addText("id", item.ID)
		entry.add("name", text(item.Name))
		entry.addText("machine", item.Machine)
		entry.add("every", text(formatDuration(item.Every)))
		entry.add("grace", text(formatDuration(item.Grace)))
		secret := block()
		secret.Style = yaml.FlowStyle
		secret.add("secret", text(SecretKept))
		entry.add("token", secret.Node)
		nodes = append(nodes, entry.Node)
	}
	return nodes
}

func ignoredNodes(keys []IgnoredKey) []*yaml.Node {
	nodes := make([]*yaml.Node, 0, len(keys))
	for _, item := range keys {
		entry := block()
		entry.add("machine", text(item.Machine))
		entry.add(string(item.Kind), text(item.Value))
		nodes = append(nodes, entry.Node)
	}
	return nodes
}
