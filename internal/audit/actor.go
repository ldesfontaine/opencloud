package audit

import "context"

// ActorKind dit qui a agi : une liste fermée.
type ActorKind string

const (
	// L'opérateur de l'interface, sous l'identité que l'authentification a
	// vue (Basic ou en-tête du mandataire) ; vide sans authentification.
	ActorOperator ActorKind = "operator"
	// Un client MCP, nommé par l'identifiant de son jeton d'API ou de sa
	// famille OAuth.
	ActorMCP ActorKind = "mcp"
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
	return []ActorKind{ActorOperator, ActorMCP, ActorCLI, ActorRule, ActorAgent, ActorUnknown}
}

// Actor est qui a agi, et sous quel nom.
type Actor struct {
	Kind ActorKind
	Name string
}

func Operator(identity string) Actor {
	return Actor{Kind: ActorOperator, Name: identity}
}

func MCP(tokenID string) Actor {
	return Actor{Kind: ActorMCP, Name: tokenID}
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
