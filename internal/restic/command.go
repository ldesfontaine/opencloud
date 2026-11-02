package restic

import (
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// L'environnement de restic : remplacé, jamais hérité. Les secrets passent
// par des fichiers 0600 que restic lit lui-même ; aucun ne figure dans un
// argument (visible de tous dans ps) ni dans une variable.
const (
	commandPath        = "PATH=/usr/sbin:/usr/bin:/sbin:/bin"
	commandLocale      = "LC_ALL=C"
	envRepository      = "RESTIC_REPOSITORY"
	envRepositoryFile  = "RESTIC_REPOSITORY_FILE"
	envPasswordFile    = "RESTIC_PASSWORD_FILE"
	envCACert          = "RESTIC_CACERT"
	maxStdinNameBytes  = 128
	maxProducerArgs    = 32
	maxProducerArgSize = 4 << 10
)

var (
	errCommandRepository = errors.New("restic command: repository incomplete for its path")
	errCommandBinary     = errors.New("restic command: binary path not absolute")
	errCommandDirectory  = errors.New("restic command: directory not absolute and clean")
	errCommandStdinName  = errors.New("restic command: stdin file name invalid")
	errCommandProducer   = errors.New("restic command: producer invalid")
	errRepositoryURL     = errors.New("restic repository: host, port or user invalid")
)

// Repository dit où restic écrit et avec quels fichiers. En direct, l'URL du
// dépôt porte le mot de passe REST : elle vit dans RepositoryFile (0600), que
// restic lit par RESTIC_REPOSITORY_FILE. Par le relais, l'URL est celle du
// mandataire local, sans mot de passe.
type Repository struct {
	Path           string
	RepositoryFile string
	CACertFile     string
	ProxyURL       string
	PasswordFile   string
	CacheDir       string
}

// Command est une commande restic prête à lancer, sans shell : le binaire,
// ses arguments et son environnement complet.
type Command struct {
	Binary string
	Args   []string
	Env    []string
}

// String rend la commande pour un journal : les arguments, et les seuls noms
// des variables. Rien d'un fichier de secret n'y entre.
func (c Command) String() string {
	names := make([]string, 0, len(c.Env))
	for _, variable := range c.Env {
		name, _, _ := strings.Cut(variable, "=")
		names = append(names, name)
	}
	return c.Binary + " " + strings.Join(c.Args, " ") + " [env " + strings.Join(names, " ") + "]"
}

// DirectRepositoryURL rend l'URL du dépôt sur la cible, mot de passe REST
// compris : à écrire dans le fichier du dépôt, jamais ailleurs. Avec
// --private-repos, le dépôt est sous le nom de l'utilisateur. Une erreur ne
// reprend aucune valeur.
func DirectRepositoryURL(host string, port int, user, restPassword string) (string, error) {
	if host == "" || strings.ContainsAny(host, "/@[]") || port < 1 || port > 65535 || !agentwire.IsResticUser(user) {
		return "", errRepositoryURL
	}
	location := url.URL{
		Scheme: "https",
		User:   url.UserPassword(user, restPassword),
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + user + "/",
	}
	return "rest:" + location.String(), nil
}

// BackupDirectory sauvegarde un dossier.
func BackupDirectory(binary string, repository Repository, directory string) (Command, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return Command{}, errCommandDirectory
	}
	return backupCommand(binary, repository, []string{"--", directory})
}

var stdinNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// BackupStream sauvegarde la sortie d'un producteur (vzdump --stdout) sous
// un nom de fichier. restic lance le producteur lui-même : s'il échoue, aucun
// instantané n'est créé (vérifié par l'essai).
func BackupStream(binary string, repository Repository, filename string, producer []string) (Command, error) {
	if len(filename) > maxStdinNameBytes || !stdinNamePattern.MatchString(filename) {
		return Command{}, errCommandStdinName
	}
	if err := validateProducer(producer); err != nil {
		return Command{}, err
	}
	args := []string{"--stdin-from-command", "--stdin-filename", filename, "--"}
	return backupCommand(binary, repository, append(args, producer...))
}

// validateProducer : un binaire absolu, des arguments bornés ; jamais de
// shell (le producteur ne passe par aucun sh -c).
func validateProducer(producer []string) error {
	if len(producer) == 0 || len(producer) > maxProducerArgs || !filepath.IsAbs(producer[0]) {
		return errCommandProducer
	}
	for _, arg := range producer {
		if len(arg) > maxProducerArgSize || strings.ContainsRune(arg, 0) {
			return errCommandProducer
		}
	}
	return nil
}

// ExitRepositoryMissing est le code de sortie de restic (0.17 et après)
// quand le dépôt n'existe pas encore : la première sauvegarde d'une source.
const ExitRepositoryMissing = 10

// InitRepository crée le dépôt, une fois, avant la première sauvegarde. En
// écriture seule, rest-server l'accepte (POST /?create=true, puis config et
// clé) ; un second init est refusé, le dépôt n'est jamais écrasé.
func InitRepository(binary string, repository Repository) (Command, error) {
	if !filepath.IsAbs(binary) {
		return Command{}, errCommandBinary
	}
	env, err := repository.environment()
	if err != nil {
		return Command{}, err
	}
	return Command{Binary: binary, Args: []string{"init", "--json", "--quiet"}, Env: env}, nil
}

func backupCommand(binary string, repository Repository, tail []string) (Command, error) {
	if !filepath.IsAbs(binary) {
		return Command{}, errCommandBinary
	}
	env, err := repository.environment()
	if err != nil {
		return Command{}, err
	}
	// --json --quiet : une seule ligne de résumé, lue par ParseBackupSummary.
	args := []string{"backup", "--json", "--quiet"}
	if repository.CacheDir != "" {
		args = append(args, "--cache-dir", repository.CacheDir)
	} else {
		args = append(args, "--no-cache")
	}
	return Command{Binary: binary, Args: append(args, tail...), Env: env}, nil
}

// environment rend les variables du seul processus fils ; elles nomment des
// fichiers, sauf l'URL du mandataire, qui n'a pas de mot de passe.
func (r Repository) environment() ([]string, error) {
	if !filepath.IsAbs(r.PasswordFile) {
		return nil, errCommandRepository
	}
	env := []string{commandPath, commandLocale, envPasswordFile + "=" + r.PasswordFile}
	switch r.Path {
	case agentwire.ResticPathDirect:
		if !filepath.IsAbs(r.RepositoryFile) || !filepath.IsAbs(r.CACertFile) || r.ProxyURL != "" {
			return nil, errCommandRepository
		}
		return append(env, envRepositoryFile+"="+r.RepositoryFile, envCACert+"="+r.CACertFile), nil
	case agentwire.ResticPathRelay:
		if !strings.HasPrefix(r.ProxyURL, "rest:http://127.0.0.1:") || r.RepositoryFile != "" {
			return nil, errCommandRepository
		}
		return append(env, envRepository+"="+r.ProxyURL), nil
	}
	return nil, errCommandRepository
}

// La rétention se juge par hôte et par chemins : jamais un instantané d'une
// portée n'en fait oublier un d'une autre.
const forgetGroupBy = "host,paths"

// ForgetRepository applique une rétention sur un dépôt local de la cible :
// restic forget --prune, l'instantané le plus récent de chaque fenêtre gardé.
// Le dépôt est un chemin absolu (le dossier des dépôts et l'utilisateur), le
// mot de passe un fichier que restic lit lui-même. Un compte nul n'est pas
// passé ; ce que restic n'a pas reçu, il ne l'applique pas.
func ForgetRepository(binary, repositoryDir, passwordFile string, retention agentwire.ResticRetainParams) (Command, error) {
	if !filepath.IsAbs(binary) {
		return Command{}, errCommandBinary
	}
	if !filepath.IsAbs(repositoryDir) || filepath.Clean(repositoryDir) != repositoryDir {
		return Command{}, errCommandDirectory
	}
	if !filepath.IsAbs(passwordFile) {
		return Command{}, errCommandRepository
	}
	if err := retention.Validate(); err != nil {
		return Command{}, err
	}
	args := []string{"-r", repositoryDir, "--no-cache", "forget", "--prune", "--group-by", forgetGroupBy}
	args = appendKeep(args, "--keep-last", retention.KeepLast)
	args = appendKeep(args, "--keep-daily", retention.KeepDaily)
	args = appendKeep(args, "--keep-weekly", retention.KeepWeekly)
	args = appendKeep(args, "--keep-monthly", retention.KeepMonthly)
	args = appendKeep(args, "--keep-yearly", retention.KeepYearly)
	env := []string{commandPath, commandLocale, envPasswordFile + "=" + passwordFile}
	return Command{Binary: binary, Args: args, Env: env}, nil
}

func appendKeep(args []string, flag string, count int) []string {
	if count == 0 {
		return args
	}
	return append(args, flag, strconv.Itoa(count))
}

// BackupSummary est ce que le résumé JSON de restic backup dit d'utile.
type BackupSummary struct {
	SnapshotID      string  `json:"snapshot_id"`
	DataAdded       int64   `json:"data_added"`
	DurationSeconds float64 `json:"total_duration"`
}

// ParseBackupSummary lit une ligne de la sortie de restic backup --json ;
// faux si ce n'est pas le résumé.
func ParseBackupSummary(line []byte) (BackupSummary, bool) {
	var message struct {
		MessageType string `json:"message_type"`
		BackupSummary
	}
	if err := json.Unmarshal(line, &message); err != nil || message.MessageType != "summary" {
		return BackupSummary{}, false
	}
	return message.BackupSummary, true
}
