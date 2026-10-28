package audit

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ActorKind dit qui a agi : une liste fermée.
type ActorKind string

const (
	// L'opérateur de l'interface, sous l'identité que l'authentification a
	// vue (Basic ou en-tête du mandataire) ; vide sans authentification.
	ActorOperator ActorKind = "operator"
	// Un jeton d'API, nommé par son identifiant ; OnBehalfOf : pour qui
	// mcp-maison dit appeler, indicatif.
	ActorAPI ActorKind = "api"
	// La commande locale, par la socket ou sur la base, sous le compte
	// système qui l'a lancée : indicatif, pas une preuve.
	ActorCLI ActorKind = "cli"
	// Une boucle de fond ou un geste du démarrage de serve.
	ActorRule ActorKind = "rule"
	// L'agent d'une machine, nommé par l'identifiant de la machine.
	ActorAgent ActorKind = "agent"
	// Un geste appelé sans acteur dans son contexte : pas un acteur, un
	// oubli de câblage, que le journal montre au lieu de le taire.
	ActorUnknown ActorKind = "unknown"
)

func ActorKinds() []ActorKind {
	return []ActorKind{ActorOperator, ActorAPI, ActorCLI, ActorRule, ActorAgent, ActorUnknown}
}

// LegacyActorKinds sont les acteurs que le MCP écrivait jusqu'au 21 octobre
// 2026 : lus, plus jamais écrits. Le journal est en ajout seul.
func LegacyActorKinds() []ActorKind {
	return []ActorKind{"mcp"}
}

// Actor est qui a agi, et sous quel nom. OnBehalfOf ne vit jamais en
// colonne : le Recorder le copie dans les détails de la ligne. Label est le
// nom lisible de l'acteur à cet instant (le nom d'un jeton d'API) : Name
// reste son identifiant stable, Label est recopié dans la ligne pour qu'un
// jeton révoqué ou renommé garde sur ses anciens gestes le nom d'alors.
type Actor struct {
	Kind       ActorKind
	Name       string
	Label      string
	OnBehalfOf string
}

// MaxLabelRunes borne Label, comme le nom d'un jeton (apitoken.MaxNameLength).
const MaxLabelRunes = 80

func Operator(identity string) Actor {
	return Actor{Kind: ActorOperator, Name: identity}
}

func API(tokenID, tokenName, onBehalfOf string) Actor {
	return Actor{Kind: ActorAPI, Name: tokenID, Label: tokenName, OnBehalfOf: onBehalfOf}
}

// cleanLabel rend un libellé affichable : sans caractère de contrôle, coupé.
func cleanLabel(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, text)
	if runes := []rune(text); len(runes) > MaxLabelRunes {
		text = string(runes[:MaxLabelRunes])
	}
	return strings.TrimSpace(text)
}

func CLI(account string) Actor {
	return Actor{Kind: ActorCLI, Name: account}
}

func Rule(name string) Actor {
	return Actor{Kind: ActorRule, Name: name}
}

func Agent(machineID string) Actor {
	return Actor{Kind: ActorAgent, Name: machineID}
}

type actorKey struct{}

type applyKey struct{}

// WithActor pose l'acteur dans le contexte : les composants le lisent sans
// que la signature de chaque geste ait à le porter.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// ActorFrom rend l'acteur du contexte, ActorUnknown s'il n'y en a pas.
func ActorFrom(ctx context.Context) Actor {
	actor, ok := ctx.Value(actorKey{}).(Actor)
	if !ok || actor.Kind == "" {
		return Actor{Kind: ActorUnknown}
	}
	return actor
}

// WithApply pose le passage d'apply en cours : chaque objet qu'il écrit
// garde le lien vers sa ligne applies.
func WithApply(ctx context.Context, applyID string) context.Context {
	return context.WithValue(ctx, applyKey{}, applyID)
}

func applyFrom(ctx context.Context) string {
	applyID, _ := ctx.Value(applyKey{}).(string)
	return applyID
}
