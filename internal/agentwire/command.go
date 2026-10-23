package agentwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Kind nomme ce que le serveur demande à un agent : une liste fermée. Les
// genres d'observation lisent la machine et voyagent en clair sur le flux,
// sous leur propre nom d'événement ; les ordres agissent et voyagent dans
// l'enveloppe signée (EventOrder). Les noms d'observation sont figés par les
// agents installés.
type Kind string

const (
	KindLogs     Kind = "logs"
	KindLogsStop Kind = "logs_stop"
	KindProbes   Kind = "probes"
	KindImages   Kind = "image_checks"
	// Il ne change rien sur la machine et prouve le chemin d'un ordre de bout
	// en bout (signature, journal, réponse).
	KindAgentPing Kind = "agent.ping"
	// Le premier geste (décision 14) : l'agent télécharge sa nouvelle version
	// depuis le serveur (AgentUpdateParams), la vérifie, bascule son lien
	// current et demande à être relancé.
	KindAgentUpdate Kind = "agent.update"
	// La rotation de la clé du serveur : signé par la clé que l'agent a
	// épinglée, il lui livre la neuve (ServerKeyParams). Le serveur l'envoie
	// de lui-même, à l'ouverture du flux d'un agent resté sur la précédente.
	KindServerKey Kind = "agent.server_key"
	// Écrit sous son chemin les fichiers d'un colis que l'agent télécharge.
	KindBackupReceive Kind = "backup.receive"
	// Garde les N sauvegardes les plus récentes du chemin, efface les autres.
	KindBackupPrune Kind = "backup.prune"
	// Écrit le compose.yml d'un service et lance docker compose up : le
	// premier déploiement (le dossier du service n'existe pas), puis les
	// suivants (il existe).
	KindDockerDeploy Kind = "docker.deploy"
	KindDockerUpdate Kind = "docker.update"
)

// ServerKeyParams sont les paramètres de agent.server_key : la clé publique
// neuve, en base64. Ce n'est pas un secret.
type ServerKeyParams struct {
	PublicKey string `json:"public_key"`
}

// ObservationKinds rend les genres qui ne font que lire.
func ObservationKinds() []Kind {
	return []Kind{KindLogs, KindLogsStop, KindProbes, KindImages}
}

// OrderKinds rend les genres d'ordres, signés et journalisés.
func OrderKinds() []Kind {
	return []Kind{KindAgentPing, KindAgentUpdate, KindServerKey, KindBackupReceive, KindBackupPrune, KindDockerDeploy, KindDockerUpdate}
}

func (k Kind) IsOrder() bool {
	return slices.Contains(OrderKinds(), k)
}

// EventOrder est le nom d'événement de l'enveloppe signée d'un ordre.
const EventOrder = "order"

// Command est ce que le serveur pousse par le flux : un genre de la liste
// et son contenu typé. Un seul champ est rempli, celui du genre.
type Command struct {
	Kind   Kind
	Logs   *container.LogRequest
	Probes *probe.Assignment
	Images *update.Assignment
	Order  *SignedOrder
}

func LogsCommand(request container.LogRequest) Command {
	return Command{Kind: KindLogs, Logs: &request}
}

func StopLogsCommand(requestID string) Command {
	return Command{Kind: KindLogsStop, Logs: &container.LogRequest{ID: requestID}}
}

func ProbesCommand(assignment probe.Assignment) Command {
	return Command{Kind: KindProbes, Probes: &assignment}
}

func ImagesCommand(assignment update.Assignment) Command {
	return Command{Kind: KindImages, Images: &assignment}
}

// OrderCommand porte un ordre signé ; son genre est celui du corps signé.
func OrderCommand(kind Kind, order SignedOrder) Command {
	return Command{Kind: kind, Order: &order}
}

var (
	ErrUnknownCommand = errors.New("unknown command")
	ErrEmptyCommand   = errors.New("command without its content")
)

// Encode rend le nom d'événement et le contenu JSON, sur une ligne.
func (c Command) Encode() (event string, data []byte, err error) {
	var content any
	event = string(c.Kind)
	switch {
	case c.Kind.IsOrder():
		event, content = EventOrder, c.Order
		if c.Order == nil {
			return "", nil, fmt.Errorf("%s: %w", c.Kind, ErrEmptyCommand)
		}
	case c.Kind == KindLogs, c.Kind == KindLogsStop:
		content = c.Logs
		if c.Logs == nil {
			return "", nil, fmt.Errorf("%s: %w", c.Kind, ErrEmptyCommand)
		}
	case c.Kind == KindProbes:
		content = c.Probes
		if c.Probes == nil {
			return "", nil, fmt.Errorf("%s: %w", c.Kind, ErrEmptyCommand)
		}
	case c.Kind == KindImages:
		content = c.Images
		if c.Images == nil {
			return "", nil, fmt.Errorf("%s: %w", c.Kind, ErrEmptyCommand)
		}
	default:
		return "", nil, fmt.Errorf("%q: %w", c.Kind, ErrUnknownCommand)
	}
	data, err = json.Marshal(content)
	return event, data, err
}

// DecodeObservation lit une commande d'observation reçue par le flux. Un
// ordre ne passe pas par ici : son genre ne se lit qu'une fois la signature
// vérifiée (DecodeSignedOrder).
func DecodeObservation(event string, data []byte) (Command, error) {
	command := Command{Kind: Kind(event)}
	var err error
	switch command.Kind {
	case KindLogs, KindLogsStop:
		command.Logs = &container.LogRequest{}
		err = json.Unmarshal(data, command.Logs)
	case KindProbes:
		command.Probes = &probe.Assignment{}
		err = json.Unmarshal(data, command.Probes)
	case KindImages:
		command.Images = &update.Assignment{}
		err = json.Unmarshal(data, command.Images)
	default:
		return Command{}, fmt.Errorf("%q: %w", event, ErrUnknownCommand)
	}
	if err != nil {
		return Command{}, fmt.Errorf("decode %s: %w", event, err)
	}
	return command, nil
}
