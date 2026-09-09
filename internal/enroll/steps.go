package enroll

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// playEnroler joue le script de l'action Enrôler, en root, sur cette machine.
// La séquence — compte, sudo, sshd, clé — est écrite une seule fois, dans
// internal/scripts/enroler/run.sh : c'est le même script qu'un opérateur colle
// sur une machine distante (15-catalogue-actions.md §3).
func (e *enrollment) playEnroler(ctx context.Context) error {
	prepared, err := catalog.Prepare(catalog.KindEnroler, map[string]string{
		publicKeyParam: strings.TrimSpace(e.publicKey),
	})
	if err != nil {
		return err
	}

	outcome := &scriptOutcome{out: e.deps.Out}
	exitCode, err := e.deps.Scripts.Run(ctx, LocalScript{
		Content:     prepared.Script,
		Environment: environmentOf(prepared),
		Timeout:     prepared.Definition.Timeout,
	}, outcome.read)
	if err != nil {
		return err
	}
	return e.readOutcome(exitCode, outcome)
}

// readOutcome traduit le constat du script : ce que l'opérateur lit d'un refus
// vient du script, mot pour mot.
func (e *enrollment) readOutcome(exitCode int, outcome *scriptOutcome) error {
	switch exitCode {
	case catalog.ExitDone:
		switch outcome.result {
		case resultDone:
			e.steps.markChanged()
			return nil
		case resultUnchanged:
			return nil
		default:
			return fmt.Errorf("la séquence d'enrôlement s'est terminée sans constat")
		}
	case catalog.ExitRefused:
		return refusal.Refusal{Cause: outcome.cause(), Remedy: outcome.remedy}
	default:
		return fmt.Errorf("la séquence d'enrôlement a échoué : %s", outcome.cause())
	}
}

// installLauncher pose le lanceur venu du paquet. Il ne voyage pas dans la
// commande d'une machine distante — il fait plusieurs mébioctets — et vient
// donc après la séquence, ici comme là-bas (15-catalogue-actions.md §3).
func (e *enrollment) installLauncher(_ context.Context) error {
	packaged, err := os.ReadFile(e.systemPath(packagedLauncher)) // bounded: chemin dérivé de la racine système
	if err != nil {
		return fmt.Errorf("lire le lanceur du paquet : %w", err)
	}

	directory := e.systemPath(launcherDir)
	target := filepath.Join(directory, launcherName)
	installed, found, err := readIfExists(target)
	if err != nil {
		return err
	}
	if found && bytes.Equal(installed, packaged) {
		e.steps.unchanged("lanceur /%s/%s", launcherDir, launcherName)
		return nil
	}

	if err := os.MkdirAll(directory, 0o755); err != nil { // #nosec G301 -- /usr/local/sbin est lisible de tous, comme tout /usr/local/sbin
		return fmt.Errorf("créer %s : %w", directory, err)
	}
	if err := writeSystemFile(directory, launcherName, packaged, 0o755); err != nil {
		return err
	}
	if err := e.deps.Chown(target, 0, 0); err != nil {
		return fmt.Errorf("donner %s à root : %w", target, err)
	}
	e.steps.done("lanceur /%s/%s posé", launcherDir, launcherName)
	return nil
}

// prepareIdentity tient la paire de clés de la machine openCloud : le script
// ne peut pas l'engendrer, c'est openCloud qui la garde.
func (e *enrollment) prepareIdentity(_ context.Context) error {
	if err := e.ensureMachineDir(); err != nil {
		return err
	}
	return e.ensureIdentity()
}

// ensureMachineDir crée machines/ et machines/local et les donne tous deux
// au compte : l'amorçage tourne en root, et un parent resté à root
// empêcherait le service de traverser jusqu'à sa clé.
func (e *enrollment) ensureMachineDir() error {
	for _, directory := range []string{machinesDirName, MachineDir(LocalMachineID)} {
		if err := e.deps.Root.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("créer %s : %w", directory, err)
		}
		if err := e.deps.Root.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("fermer %s : %w", directory, err)
		}
		if err := e.ownState(directory); err != nil {
			return err
		}
	}
	return nil
}

func (e *enrollment) ensureIdentity() error {
	directory := MachineDir(LocalMachineID)
	privateName := path.Join(directory, identityFileName)
	publicName := path.Join(directory, publicKeyFileName)

	existing, found, err := readRootFile(e.deps.Root, privateName)
	if err != nil {
		return err
	}
	if found {
		e.publicKey, err = publicLineOf(existing, LocalMachineID)
		if err != nil {
			return err
		}
		e.steps.unchanged("clé de la machine %s", LocalMachineID)
	} else {
		privateKey, publicLine, err := generateIdentity(LocalMachineID)
		if err != nil {
			return err
		}
		// O_EXCL : une clé posée n'est jamais remplacée, même par erreur.
		if err := e.createPrivateKey(privateName, privateKey); err != nil {
			return err
		}
		e.publicKey = publicLine
		e.steps.done("clé de la machine %s engendrée", LocalMachineID)
	}

	current, _, err := readRootFile(e.deps.Root, publicName)
	if err != nil {
		return err
	}
	if bytes.Equal(current, []byte(e.publicKey)) {
		return nil
	}
	if err := fsx.WriteFile(e.deps.Root, publicName, []byte(e.publicKey), 0o644); err != nil {
		return fmt.Errorf("écrire %s : %w", publicName, err)
	}
	return e.ownState(publicName)
}

func (e *enrollment) createPrivateKey(name string, content []byte) error {
	file, err := e.deps.Root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("créer %s : %w", name, err)
	}
	defer file.Close()

	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("écrire %s : %w", name, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("écrire %s : %w", name, err)
	}
	if err := e.deps.Root.Chmod(name, 0o600); err != nil {
		return fmt.Errorf("fermer %s : %w", name, err)
	}
	return e.ownState(name)
}

// writeKnownHosts écrit les clés d'hôte de la machine : elles ne sont jamais
// apprises à la connexion, sinon StrictHostKeyChecking ne prouverait rien. Le
// script affiche l'empreinte à un opérateur ; ici, openCloud la lit lui-même.
func (e *enrollment) writeKnownHosts(_ context.Context) error {
	content, err := knownHostsContent(e.systemPath(hostKeyDir), localAddress, localSSHPort)
	if err != nil {
		return err
	}

	name := path.Join(MachineDir(LocalMachineID), knownHostsFileName)
	current, _, err := readRootFile(e.deps.Root, name)
	if err != nil {
		return err
	}
	if bytes.Equal(current, []byte(content)) {
		e.steps.unchanged("clés d'hôte connues de %s", LocalMachineID)
		return nil
	}
	if err := fsx.WriteFile(e.deps.Root, name, []byte(content), 0o644); err != nil {
		return fmt.Errorf("écrire %s : %w", name, err)
	}
	if err := e.ownState(name); err != nil {
		return err
	}
	e.steps.done("clés d'hôte de %s écrites", LocalMachineID)
	return nil
}

// verifyRealPath prouve l'amorçage par le chemin qu'une action prendra, pas
// par la sortie des outils qu'on vient de lancer.
func (e *enrollment) verifyRealPath(ctx context.Context) error {
	endpoint := LocalEndpoint(e.deps.Root)
	endpoint.Binary = e.deps.SSHBinary
	client := e.deps.Transport(endpoint)

	if err := probeWithRetries(ctx, client, localAddress, e.deps.ProbeWait); err != nil {
		return err
	}

	reply, err := client.CheckLauncher(ctx)
	if err != nil {
		return err
	}
	if err := checkLauncherReply(reply); err != nil {
		return err
	}
	e.steps.note("vérifié par SSH vers %s : le lanceur répond derrière sudo", localAddress)
	return nil
}

func (e *enrollment) markEnrolled(_ context.Context) error {
	written, err := writeEnrolledMarker(e.deps.Root, LocalMachineID, e.deps.Now())
	if err != nil {
		return err
	}
	if !written {
		e.steps.unchanged("marqueur d'enrôlement")
		return nil
	}
	if err := e.ownState(path.Join(MachineDir(LocalMachineID), enrolledFileName)); err != nil {
		return err
	}
	e.steps.done("marqueur d'enrôlement écrit")
	return nil
}

// ownState : tout ce qui vit sous le répertoire d'état appartient au compte
// opencloud, qui est celui qui s'en sert.
func (e *enrollment) ownState(name string) error {
	if err := e.deps.Chown(filepath.Join(e.deps.Root.Name(), name), e.account.UID, e.account.GID); err != nil {
		return fmt.Errorf("donner %s à %s : %w", name, AccountName, err)
	}
	return nil
}

func readIfExists(name string) ([]byte, bool, error) {
	content, err := os.ReadFile(name) // #nosec G304 -- bounded: chemin dérivé de la racine système et de constantes
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lire %s : %w", name, err)
	}
	return content, true, nil
}

func readRootFile(root *os.Root, name string) ([]byte, bool, error) {
	content, err := root.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("lire %s : %w", name, err)
	}
	return content, true, nil
}

// writeSystemFile pose un fichier hors du répertoire d'état, atomiquement.
func writeSystemFile(directory, name string, content []byte, mode os.FileMode) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("ouvrir %s : %w", directory, err)
	}
	defer root.Close()

	if err := fsx.WriteFile(root, name, content, mode); err != nil {
		return fmt.Errorf("écrire %s : %w", filepath.Join(directory, name), err)
	}
	return root.Chmod(name, mode)
}
