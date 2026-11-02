package dockerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// L'environnement d'un conteneur (étude services-modèle §2.4). Docker le rend
// en clair, secrets compris : « ["DB_PASSWORD=hunter2", "DB_HOST=db"] ».
// Container ne le déclare pas, donc ne peut pas le porter. Il ne se lit que
// par InspectEnvironment, qu'on n'appelle qu'avec le droit observe.env, et ce
// qu'il rend (EnvHosts) ne garde que des noms d'hôtes candidats : jamais le
// nom d'une variable, jamais une valeur.
const (
	// maxEnvValue : une valeur plus longue n'est pas une adresse.
	maxEnvValue = 1024
	// MaxEnvCandidates borne ce qu'un conteneur peut faire retenir : une image
	// à mille variables ne remplit pas la mémoire de l'agent.
	MaxEnvCandidates = 256
	maxHostLength    = 253
)

// EnvHost est un nom d'hôte tel qu'une variable le portait, en minuscules, et
// son port (0 : aucun).
type EnvHost struct {
	Name string
	Port int
}

// EnvHosts est tout ce qu'on garde de l'environnement. Son décodage JSON jette
// les noms de variables et les valeurs à mesure : il ne reste que les hôtes
// candidats, sans doublon. Le type ne se sérialise pas, ne s'écrit pas au
// journal et ne s'affiche pas : on ne peut pas, par erreur, le faire sortir.
type EnvHosts struct {
	hosts []EnvHost
}

// Hosts rend les candidats, dans l'ordre où l'environnement les portait.
func (e EnvHosts) Hosts() []EnvHost {
	return e.hosts
}

// UnmarshalJSON lit la liste « NOM=valeur » entrée par entrée : chaque entrée
// est jugée puis oubliée avant la suivante.
func (e *EnvHosts) UnmarshalJSON(data []byte) error {
	e.hosts = nil
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil {
		return errors.New("environment is not readable")
	}
	if opening == nil {
		return nil
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '[' {
		return errors.New("environment is not a list")
	}
	seen := map[EnvHost]bool{}
	for decoder.More() {
		var entry string
		if err := decoder.Decode(&entry); err != nil {
			return errors.New("environment entry is not a string")
		}
		_, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		host, ok := hostOf(value)
		if !ok || seen[host] || len(e.hosts) >= MaxEnvCandidates {
			continue
		}
		seen[host] = true
		e.hosts = append(e.hosts, host)
	}
	return nil
}

// MarshalJSON refuse : l'environnement ne part jamais, sous aucune forme.
func (EnvHosts) MarshalJSON() ([]byte, error) {
	return nil, errors.New("environment is never serialised")
}

// String, GoString et LogValue disent la même chose : rien.
func (EnvHosts) String() string       { return "[environment withheld]" }
func (EnvHosts) GoString() string     { return "dockerapi.EnvHosts{withheld}" }
func (EnvHosts) LogValue() slog.Value { return slog.StringValue("withheld") }

var (
	// Un schéma d'URL, y compris « jdbc:postgresql ».
	envScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.:-]{0,31}$`)
	// Un nom d'hôte ou une adresse IPv4, sans rien d'autre.
	envHostName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,252}$`)
)

// hostOf extrait l'hôte d'une valeur, strictement : « schéma://[infos@]hôte[:port]… »
// (infos, chemin et requête écartés avant toute comparaison), ou « hôte[:port] »
// nu. Rien d'autre n'est analysé : une valeur qui n'a pas l'une de ces deux
// formes ne donne rien.
func hostOf(value string) (EnvHost, bool) {
	if value == "" || len(value) > maxEnvValue {
		return EnvHost{}, false
	}
	authority := value
	if scheme, rest, isURL := strings.Cut(value, "://"); isURL {
		if !envScheme.MatchString(scheme) {
			return EnvHost{}, false
		}
		authority, _, _ = strings.Cut(rest, "/")
		authority, _, _ = strings.Cut(authority, "?")
		authority, _, _ = strings.Cut(authority, "#")
		if at := strings.LastIndex(authority, "@"); at >= 0 {
			authority = authority[at+1:]
		}
	}
	name, port := authority, 0
	if before, after, found := strings.Cut(authority, ":"); found {
		number, err := strconv.Atoi(after)
		if err != nil || number < 1 || number > 65535 || after != strconv.Itoa(number) {
			return EnvHost{}, false
		}
		name, port = before, number
	}
	name = strings.ToLower(name)
	if !envHostName.MatchString(name) {
		return EnvHost{}, false
	}
	return EnvHost{Name: name, Port: port}, true
}

// InspectEnvironment lit l'environnement d'un conteneur et n'en garde que les
// hôtes candidats. Un appel de plus que Inspect : l'appelant ne le fait que
// pour une machine dont l'agent a le droit observe.env.
func (c *Client) InspectEnvironment(ctx context.Context, id string) (EnvHosts, error) {
	var inspected struct {
		Config struct {
			Env EnvHosts
		}
	}
	if err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/json", &inspected); err != nil {
		return EnvHosts{}, err
	}
	return inspected.Config.Env, nil
}
