package dockerapi

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// Les labels que Compose pose, et le seul qu'on lit pour exclure :
// « docker compose run » fait un conteneur jetable avec les labels du service.
const (
	LabelComposeProject = "com.docker.compose.project"
	LabelComposeService = "com.docker.compose.service"
	LabelComposeOneOff  = "com.docker.compose.oneoff"
)

// Summary est une ligne de la liste ; seuls les champs lus sont déclarés.
type Summary struct {
	ID      string `json:"Id"`
	Names   []string
	Image   string
	ImageID string
	State   string
	Created int64
	Labels  map[string]string
	Ports   []PublishedPort
}

// PublishedPort est un port publié sur l'hôte tel que la liste le donne.
type PublishedPort struct {
	IP          string
	PrivatePort int
	PublicPort  int
	Type        string
}

// Name rend le nom sans la barre oblique que Docker met devant.
func (s Summary) Name() string {
	if len(s.Names) == 0 {
		return ""
	}
	return strings.TrimPrefix(s.Names[0], "/")
}

// IsOneOff dit si le conteneur vient de « docker compose run ».
func (s Summary) IsOneOff() bool {
	return strings.EqualFold(s.Labels[LabelComposeOneOff], "true")
}

// Container est un conteneur inspecté ; là aussi, les seuls champs lus.
type Container struct {
	ID           string `json:"Id"`
	Name         string
	Image        string
	Created      string
	RestartCount int
	State        ContainerState
	Config       ContainerConfig
	Network      NetworkSettings `json:"NetworkSettings"`
}

type ContainerState struct {
	Status     string
	ExitCode   int
	OOMKilled  bool
	StartedAt  string
	FinishedAt string
	Health     *Health
}

type Health struct {
	Status string
}

type ContainerConfig struct {
	Image       string
	Tty         bool
	Labels      map[string]string
	Healthcheck *Healthcheck
}

type Healthcheck struct {
	Test []string
}

type NetworkSettings struct {
	// La clé est « 80/tcp » ; la valeur est nulle quand le port n'est pas
	// publié.
	Ports map[string][]PortBinding
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string
}

// HasHealthcheck dit si l'image ou la commande définit un HEALTHCHECK.
func (c Container) HasHealthcheck() bool {
	return c.Config.Healthcheck != nil && len(c.Config.Healthcheck.Test) > 0
}

// ShortName rend le nom sans la barre oblique.
func (c Container) ShortName() string {
	return strings.TrimPrefix(c.Name, "/")
}

// ListContainers rend tous les conteneurs de la machine, arrêtés compris.
func (c *Client) ListContainers(ctx context.Context) ([]Summary, error) {
	var list []Summary
	if err := c.getJSON(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (c *Client) Inspect(ctx context.Context, id string) (Container, error) {
	var container Container
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/json", &container); err != nil {
		return Container{}, err
	}
	return container, nil
}

// ParseTime lit une date de Docker ; la date nulle de Docker (année 1)
// rend une time.Time zéro.
func ParseTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Year() < 1970 {
		return time.Time{}
	}
	return parsed.UTC()
}
