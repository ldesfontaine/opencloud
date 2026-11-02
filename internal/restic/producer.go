package restic

import (
	"errors"
	"path"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Les producteurs d'une sauvegarde de service (droit restic.docker) : le
// programme docker de la machine, lancé par restic (--stdin-from-command).
// Un volume se lit par un conteneur qui le monte (docker cp, en tar) ; une
// base se vide dans son conteneur (docker exec), par un script fixe du
// moteur. L'agent ne lit jamais l'environnement du conteneur : le script y
// prend lui-même les identifiants, qui ne figurent dans aucun argument.

// DumpFailedMarker est la ligne que le script d'un moteur écrit sur sa
// sortie d'erreur quand l'outil de dump échoue : restic la recopie, l'agent
// y lit dump_failed. La sortie d'erreur de l'outil, elle, est jetée : elle
// pourrait citer un identifiant que l'agent ne sait pas masquer.
const DumpFailedMarker = "opencloud-dump-failed"

// Les scripts des moteurs, sur une ligne, constants : rien d'un ordre ni de
// l'inventaire n'y entre. Les mots de passe passent par une variable
// (PGPASSWORD, MYSQL_PWD) ou un fichier 0600 du conteneur (mongodump
// --config), jamais par un argument, visible de tous dans ps.
const (
	postgresDumpScript = `export PGPASSWORD="$POSTGRES_PASSWORD"; ` +
		`pg_dumpall -U "${POSTGRES_USER:-postgres}" 2>/dev/null || { echo ` + DumpFailedMarker + ` >&2; exit 1; }`
	mariadbDumpScript = `export MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"; ` +
		`if command -v mariadb-dump >/dev/null 2>&1; then dump=mariadb-dump; else dump=mysqldump; fi; ` +
		`"$dump" -uroot --all-databases --single-transaction --routines --events 2>/dev/null || { echo ` + DumpFailedMarker + ` >&2; exit 1; }`
	mysqlDumpScript = `export MYSQL_PWD="$MYSQL_ROOT_PASSWORD"; ` +
		`mysqldump -uroot --all-databases --single-transaction --routines --events 2>/dev/null || { echo ` + DumpFailedMarker + ` >&2; exit 1; }`
	// Sans compte racine déclaré, mongod n'a pas d'authentification. Sinon le
	// mot de passe passe par un fichier YAML 0600 que mongodump lit (--config),
	// les apostrophes doublées ; sed le reçoit sur son entrée, pas en argument.
	mongoDumpScript = `if [ -z "$MONGO_INITDB_ROOT_USERNAME" ]; then ` +
		`mongodump --quiet --archive 2>/dev/null || { echo ` + DumpFailedMarker + ` >&2; exit 1; }; exit 0; fi; ` +
		`umask 077; config=$(mktemp) || { echo ` + DumpFailedMarker + ` >&2; exit 1; }; trap 'rm -f "$config"' EXIT; ` +
		`printf '%s' "$MONGO_INITDB_ROOT_PASSWORD" | sed "s/'/''/g; s/^/password: '/; s/\$/'/" > "$config"; ` +
		`mongodump --quiet --archive --authenticationDatabase admin --username "$MONGO_INITDB_ROOT_USERNAME" --config "$config" 2>/dev/null || { echo ` + DumpFailedMarker + ` >&2; exit 1; }`
)

var (
	errProducerDocker      = errors.New("restic producer: docker path not absolute")
	errProducerContainer   = errors.New("restic producer: container name invalid")
	errProducerDestination = errors.New("restic producer: mount destination not absolute and clean")
	errProducerEngine      = errors.New("restic producer: database engine unsupported")
)

// DumpScript rend le script fixe d'un moteur, vide s'il n'en a pas.
func DumpScript(engine string) string {
	switch engine {
	case agentwire.EnginePostgres:
		return postgresDumpScript
	case agentwire.EngineMariaDB:
		return mariadbDumpScript
	case agentwire.EngineMySQL:
		return mysqlDumpScript
	case agentwire.EngineMongo:
		return mongoDumpScript
	}
	return ""
}

// VolumeProducer rend « docker cp <conteneur>:<destination>/. - » : le
// contenu du volume, monté là dans le conteneur, en tar sur la sortie
// standard. Le nom du conteneur ne porte jamais de « : » (IsDockerName).
func VolumeProducer(docker, container, destination string) ([]string, error) {
	if err := validateDocker(docker, container); err != nil {
		return nil, err
	}
	if !path.IsAbs(destination) || path.Clean(destination) != destination || strings.ContainsRune(destination, 0) {
		return nil, errProducerDestination
	}
	source := container + ":" + strings.TrimSuffix(destination, "/") + "/."
	return []string{docker, "cp", source, "-"}, nil
}

// DatabaseProducer rend « docker exec <conteneur> sh -c '<script du moteur>' ».
func DatabaseProducer(docker, container, engine string) ([]string, error) {
	if err := validateDocker(docker, container); err != nil {
		return nil, err
	}
	script := DumpScript(engine)
	if script == "" {
		return nil, errProducerEngine
	}
	return []string{docker, "exec", container, "sh", "-c", script}, nil
}

func validateDocker(docker, container string) error {
	if !filepath.IsAbs(docker) {
		return errProducerDocker
	}
	if !agentwire.IsDockerName(container) {
		return errProducerContainer
	}
	return nil
}

// VolumeFileName est le nom stable, dans le dépôt, de l'instantané d'un
// volume : restic déduplique d'une passe à l'autre et la rétention le
// regroupe à part (--group-by host,paths).
func VolumeFileName(volume string) string {
	return "volume-" + volume + ".tar"
}

// DatabaseFileName est le nom stable du dump d'une base : SQL pour
// PostgreSQL, MariaDB et MySQL, archive de mongodump pour MongoDB.
func DatabaseFileName(container, engine string) string {
	if engine == agentwire.EngineMongo {
		return "database-" + container + ".archive"
	}
	return "database-" + container + ".sql"
}
