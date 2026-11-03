package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La source restic (droit restic.produce) : par cible liée, un dossier
// links/<cible>/ de l'état. Le mot de passe REST et le mot de passe du dépôt
// sont tirés et scellés par le serveur, reçus dans le secret signé de
// restic.provision, gardés ici en 0600, jamais rapportés ni écrits ailleurs.
// Un nouveau restic.provision remplace l'adresse, le certificat et les mots
// de passe : le serveur en est la référence.
const (
	linksDir           = "links"
	linkStateFile      = "link.json"
	linkCACertFile     = "ca.crt"
	linkRESTPassword   = "rest-password"
	linkRepoPassword   = "repo-password"
	linkRepositoryFile = "repository"
	linkCacheDir       = "cache"
	maxLinkStateBytes  = 16 << 10
	maxReportedLinks   = 64
)

// linkState est ce que restic.provision a posé pour une cible : rien de
// secret.
type linkState struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	User        string `json:"user"`
	Certificate string `json:"certificate"`
}

// resticSource tient les liens de la source et lance restic.
type resticSource struct {
	stateDir *os.Root
	logger   *slog.Logger
	runner   resticRunner
	// dialer compose la sonde TCP de la cible.
	dialer restic.Dialer
	// relay rend la configuration du mandataire local vers le relais du
	// serveur pour une cible : l'adresse, le client et la preuve de l'agent.
	// Posé par Run ; nil : pas de relais.
	relay func(target string) (restic.ProxyConfig, error)
	now   func() time.Time
	// inventory relit les conteneurs et les volumes de la machine (droit
	// restic.docker), dockerProgram est le docker que restic lance pour les
	// lire. Posés par newOrderRunner ; les tests y mettent les leurs.
	inventory     dockerInventory
	dockerProgram string
}

func newResticSource(stateDir *os.Root, logger *slog.Logger) *resticSource {
	return &resticSource{
		stateDir: stateDir, logger: logger, runner: execResticRunner{},
		dialer: &net.Dialer{}, now: time.Now, dockerProgram: dockerBinary,
	}
}

// openLinkDir ouvre (et crée) links/<cible>/ ; le nom est jugé par
// agentwire.IsResticUser, un seul segment sans point.
func (s *resticSource) openLinkDir(target string) (*os.Root, error) {
	if !agentwire.IsResticUser(target) {
		return nil, errors.New("restic target name invalid")
	}
	resticRoot, err := openResticDir(s.stateDir)
	if err != nil {
		return nil, err
	}
	defer resticRoot.Close()
	if err := resticRoot.MkdirAll(path.Join(linksDir, target), resticDirMode); err != nil {
		return nil, fmt.Errorf("create link dir: %w", err)
	}
	return resticRoot.OpenRoot(path.Join(linksDir, target))
}

// provision fait restic.provision : le certificat épinglé, l'adresse, les
// deux mots de passe reçus et le fichier du dépôt.
func (s *resticSource) provision(params agentwire.ResticProvisionParams, secret agentwire.ResticProvisionSecret) ([]agentwire.OutputLine, error) {
	repositoryURL, err := restic.DirectRepositoryURL(params.Host, params.Port, params.User, secret.RESTPassword)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	link, err := s.openLinkDir(params.Target)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer link.Close()
	if err := fsx.WriteAtomic(link, linkRESTPassword, []byte(secret.RESTPassword), resticFileMode); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("write %s: %w", linkRESTPassword, err))
	}
	if err := fsx.WriteAtomic(link, linkRepoPassword, []byte(secret.RepoPassword), resticFileMode); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("write %s: %w", linkRepoPassword, err))
	}
	state := linkState{Host: params.Host, Port: params.Port, User: params.User, Certificate: params.Certificate}
	if err := writeLink(link, state, repositoryURL); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	address := net.JoinHostPort(params.Host, strconv.Itoa(params.Port))
	return []agentwire.OutputLine{resticOutput("linked to " + address + " as " + params.User)}, nil
}

func readLinkState(link *os.Root) (linkState, error) {
	content, err := fsx.ReadPrivate(link, linkStateFile, maxLinkStateBytes)
	if err != nil {
		return linkState{}, err
	}
	var state linkState
	if err := json.Unmarshal(content, &state); err != nil {
		return linkState{}, fmt.Errorf("parse link state: %w", err)
	}
	return state, nil
}

// writeLink écrit le certificat, le fichier du dépôt (l'URL porte le mot de
// passe REST : 0600, lu par restic seul) puis l'état, en dernier : un lien à
// moitié écrit n'a pas d'état et ne sert pas.
func writeLink(link *os.Root, state linkState, repositoryURL string) error {
	if err := fsx.WriteAtomic(link, linkCACertFile, []byte(state.Certificate), resticFileMode); err != nil {
		return fmt.Errorf("write link certificate: %w", err)
	}
	if err := fsx.WriteAtomic(link, linkRepositoryFile, []byte(repositoryURL), resticFileMode); err != nil {
		return fmt.Errorf("write repository file: %w", err)
	}
	content, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode link state: %w", err)
	}
	return fsx.WriteAtomic(link, linkStateFile, content, resticFileMode)
}

// loadTarget rend ce que la source sait d'une cible pour la sonder ; zéro
// sans restic.provision, et la sauvegarde part par le relais.
func (s *resticSource) loadTarget(target string) restic.Target {
	link, err := s.openLinkDir(target)
	if err != nil {
		return restic.Target{}
	}
	defer link.Close()
	state, err := readLinkState(link)
	if err != nil {
		return restic.Target{}
	}
	return restic.Target{Host: state.Host, Port: state.Port, CertificatePEM: state.Certificate}
}

// isProvisioned dit si la source a reçu les mots de passe d'une cible : sans
// eux, elle ne sauvegarde pas.
func (s *resticSource) isProvisioned(target string) bool {
	link, err := s.openLinkDir(target)
	if err != nil {
		return false
	}
	defer link.Close()
	if _, err := readLinkState(link); err != nil {
		return false
	}
	_, err = readLinkSecret(link, linkRepoPassword)
	return err == nil
}

// readLinkSecret relit un mot de passe du lien ; un fichier lisible d'autres
// comptes, ou hors forme, est refusé.
func readLinkSecret(link *os.Root, name string) (string, error) {
	content, err := fsx.ReadPrivate(link, name, maxSecretFileBytes)
	if err != nil {
		return "", err
	}
	if !agentwire.IsResticPassword(string(content)) {
		return "", fmt.Errorf("%s is not in its form", name)
	}
	return string(content), nil
}

// report rend la section source du signal : la version de restic et les
// cibles liées, par leur nom. Jamais un mot de passe ni une empreinte.
func (s *resticSource) report(ctx context.Context, checks hostChecks) agentwire.ResticSourceReport {
	var report agentwire.ResticSourceReport
	if binary, err := checks.findRestic(); err == nil {
		report.Version = readBinaryVersion(ctx, binary, "version")
	}
	resticRoot, err := openResticDir(s.stateDir)
	if err != nil {
		return report
	}
	defer resticRoot.Close()
	entries, err := fs.ReadDir(resticRoot.FS(), linksDir)
	if err != nil {
		return report
	}
	for _, entry := range entries {
		if len(report.Targets) >= maxReportedLinks {
			break
		}
		if entry.IsDir() && agentwire.IsResticUser(entry.Name()) && s.isProvisioned(entry.Name()) {
			report.Targets = append(report.Targets, entry.Name())
		}
	}
	return report
}
