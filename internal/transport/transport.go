package transport

import (
	"context"
	"errors"
	"io/fs"
	"time"
)

// Line est une ligne de journald pour l'unité de l'action : ce que le script
// a écrit, horodaté par la machine. Cursor permet de reprendre après elle.
type Line struct {
	At     time.Time
	Text   string
	Cursor string
}

// Outcome est ce que systemd a constaté à la fin de l'unité. ExitCode est
// celui du script ; Killed dit qu'un signal l'a arrêté, TimedOut que c'est
// RuntimeMaxSec qui l'a fait.
type Outcome struct {
	ExitCode int
	Killed   bool
	TimedOut bool
}

var (
	// ErrUnreachable : la connexion n'a pas abouti — réseau, clé, hôte
	// inconnu. On ne sait rien pour l'instant ; ce n'est pas un échec de l'action.
	ErrUnreachable = errors.New("machine unreachable")
	// ErrAlreadyLaunched : l'unité existe déjà — l'action est partie une
	// première fois ; suivre, ne pas relancer.
	ErrAlreadyLaunched = errors.New("action already launched")
	// ErrLaunchRefused : le lanceur ou systemd-run a refusé, en le disant.
	ErrLaunchRefused = errors.New("launch refused")
)

// Transport est ce que runner attend d'une machine. Les chemins sont ceux de
// actiondir ; name est run.sh, params.env, timeout, ou files/<chemin>.
type Transport interface {
	// Put dépose un fichier sous le dossier de l'action, atomiquement, avec
	// son mode. Le dossier est créé s'il manque.
	Put(ctx context.Context, actionID, name string, content []byte, mode fs.FileMode) error
	// Launch joue le vecteur fixe : sudo -n /usr/local/sbin/oc-launch <id>.
	// Il rend le nombre d'anciens dossiers d'action que le lanceur a purgés
	// au passage, zéro s'il n'a rien purgé.
	Launch(ctx context.Context, actionID string) (purgedDirectories int, err error)
	// Follow lit journald pour l'unité, depuis le début ou après afterCursor,
	// appelle emit à chaque ligne, et rend l'issue une fois l'unité finie.
	// Il bloque jusque-là ou jusqu'à ctx.
	Follow(ctx context.Context, actionID, afterCursor string, emit func(Line)) (Outcome, error)
	// Probe vérifie que la machine répond : ssh true. Sans effet.
	Probe(ctx context.Context) error
}
