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
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// La règle sudo : le compte opencloud n'a droit qu'au lanceur, et son
// environnement ne le suit pas (annexes/lecture-sudoers.md).
const sudoersContent = "Defaults:opencloud env_reset, !setenv, !log_input, !log_stdin\n" +
	"opencloud ALL=(root) NOPASSWD: /usr/local/sbin/oc-launch\n"

// Le drop-in sshd. « Match all » referme le bloc : le drop-in est inclus en
// tête de sshd_config, et sans lui tout ce qui suit l'Include ne vaudrait plus
// que pour le compte opencloud — sshd -t le refuserait.
const sshdDropInContent = `# Posé par « opencloud enroll-local ». Le compte de service ne fait que
# lancer oc-launch : il n'a besoin de rien d'autre.
Match User opencloud
    AuthenticationMethods publickey
    PasswordAuthentication no
    PermitTTY no
    X11Forwarding no
    AllowAgentForwarding no
    AllowTcpForwarding no
    PermitTunnel no
Match all
`

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

func (e *enrollment) prepareAccount(ctx context.Context) error {
	if e.account.Shell == accountShell {
		e.steps.unchanged("shell du compte %s", AccountName)
	} else {
		result, err := e.deps.Commands.Run(ctx, commandPath(usermodPath), "-s", accountShell, AccountName)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("donner %s au compte %s : %s", accountShell, AccountName, result.Output)
		}
		e.steps.done("shell du compte %s mis à %s", AccountName, accountShell)
	}

	status, err := e.deps.Commands.Run(ctx, commandPath(passwdCommandPath), "-S", AccountName)
	if err != nil {
		return err
	}
	if status.ExitCode != 0 {
		return fmt.Errorf("lire l'état du mot de passe de %s : %s", AccountName, status.Output)
	}
	if passwordLocked(status.Output) {
		e.steps.unchanged("mot de passe du compte %s verrouillé", AccountName)
	} else {
		locked, err := e.deps.Commands.Run(ctx, commandPath(passwdCommandPath), "-l", AccountName)
		if err != nil {
			return err
		}
		if locked.ExitCode != 0 {
			return fmt.Errorf("verrouiller le mot de passe de %s : %s", AccountName, locked.Output)
		}
		e.steps.done("mot de passe du compte %s verrouillé", AccountName)
	}

	return e.joinJournalGroup(ctx)
}

// joinJournalGroup : le suivi d'une action lit journalctl -u oc-action-<id>,
// une unité système ; sans systemd-journal, le compte ne verrait que le sien.
func (e *enrollment) joinJournalGroup(ctx context.Context) error {
	member, err := groupHasMember(e.systemPath(groupPath), journalGroupName, AccountName)
	if errors.Is(err, errGroupNotFound) {
		return refusal.Refusal{
			Cause:  "le groupe " + journalGroupName + " n'existe pas : sans lui, le compte opencloud ne peut pas lire le journal des actions",
			Remedy: "vérifier que systemd-journald est installé sur cette machine, puis rejouer l'amorçage",
		}
	}
	if err != nil {
		return err
	}
	if member {
		e.steps.unchanged("compte %s dans le groupe %s", AccountName, journalGroupName)
		return nil
	}

	result, err := e.deps.Commands.Run(ctx, commandPath(usermodPath), "-aG", journalGroupName, AccountName)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("ajouter %s au groupe %s : %s", AccountName, journalGroupName, result.Output)
	}
	e.steps.done("compte %s ajouté au groupe %s", AccountName, journalGroupName)
	return nil
}

func (e *enrollment) writeSudoers(ctx context.Context) error {
	directory := e.systemPath(sudoersDir)
	target := filepath.Join(directory, sudoersName)

	current, found, err := readIfExists(target)
	if err != nil {
		return err
	}
	if found && bytes.Equal(current, []byte(sudoersContent)) {
		e.steps.unchanged("règle sudo /%s/%s", sudoersDir, sudoersName)
		return nil
	}

	// Le nom porte un point : sudo ignore ce fichier tant que visudo ne l'a pas
	// validé et qu'il n'est pas renommé.
	temporary := filepath.Join(directory, sudoersName+".tmp")
	if err := writeCandidate(temporary, []byte(sudoersContent), 0o440); err != nil {
		return err
	}
	defer os.Remove(temporary) // sans effet une fois renommé

	result, err := e.deps.Commands.Run(ctx, commandPath(visudoPath), "-c", "-f", temporary)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return refusal.Refusal{
			Cause:  "visudo refuse la règle sudo d'openCloud : " + result.Output,
			Remedy: "corriger /etc/sudoers ou les autres drop-ins de /etc/sudoers.d, puis rejouer l'amorçage",
		}
	}
	if err := os.Rename(temporary, target); err != nil {
		return fmt.Errorf("poser %s : %w", target, err)
	}
	if err := e.deps.Chown(target, 0, 0); err != nil {
		return fmt.Errorf("donner %s à root : %w", target, err)
	}
	e.steps.done("règle sudo /%s/%s posée", sudoersDir, sudoersName)
	return nil
}

func (e *enrollment) configureSSHD(ctx context.Context) error {
	directory := e.systemPath(sshdDropInDir)
	target := filepath.Join(directory, sshdDropInName)

	previous, found, err := readIfExists(target)
	if err != nil {
		return err
	}
	if found && bytes.Equal(previous, []byte(sshdDropInContent)) {
		e.steps.unchanged("drop-in sshd /%s/%s", sshdDropInDir, sshdDropInName)
		return nil
	}

	if err := writeSystemFile(directory, sshdDropInName, []byte(sshdDropInContent), 0o644); err != nil {
		return err
	}
	check, err := e.deps.Commands.Run(ctx, commandPath(sshdPath), "-t")
	if err != nil {
		return err
	}
	if check.ExitCode != 0 {
		// La configuration de sshd est remise telle qu'elle était : un drop-in
		// invalide empêcherait sshd de redémarrer plus tard, sans prévenir.
		if restoreErr := restore(directory, sshdDropInName, previous, found); restoreErr != nil {
			return restoreErr
		}
		return refusal.Refusal{
			Cause:  "sshd refuse sa configuration une fois le drop-in d'openCloud posé : " + check.Output,
			Remedy: "corriger /etc/ssh/sshd_config, puis rejouer l'amorçage — le drop-in a été retiré",
		}
	}
	e.steps.done("drop-in sshd /%s/%s posé", sshdDropInDir, sshdDropInName)

	return e.reloadSSHD(ctx)
}

func (e *enrollment) reloadSSHD(ctx context.Context) error {
	active, err := e.deps.Commands.Run(ctx, commandPath(systemctlPath), "is-active", sshServiceUnitName)
	if err != nil {
		return err
	}

	arguments := []string{"enable", "--now", sshServiceUnitName}
	action := "démarré"
	if active.ExitCode == 0 {
		arguments = []string{"reload", sshServiceUnitName}
		action = "rechargé"
	}

	result, err := e.deps.Commands.Run(ctx, commandPath(systemctlPath), arguments...)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("%s %s : %s", arguments[0], sshServiceUnitName, result.Output)
	}
	e.steps.done("%s %s", sshServiceUnitName, action)
	return nil
}

// installKey pose la clé en dernier : tant qu'elle n'est pas là, personne ne
// peut se servir de ce que les étapes précédentes ont ouvert.
func (e *enrollment) installKey(_ context.Context) error {
	if err := e.ensureMachineDir(); err != nil {
		return err
	}
	if err := e.ensureIdentity(); err != nil {
		return err
	}
	if err := e.ensureAuthorizedKeys(); err != nil {
		return err
	}
	return e.ensureKnownHosts()
}

func (e *enrollment) ensureMachineDir() error {
	directory := MachineDir(LocalMachineID)
	if err := e.deps.Root.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("créer %s : %w", directory, err)
	}
	if err := e.deps.Root.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("fermer %s : %w", directory, err)
	}
	return e.ownState(directory)
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

// ensureAuthorizedKeys écrit exactement cette clé, puis relit : ce qui compte
// n'est pas ce qu'on a écrit, c'est ce que sshd lira.
func (e *enrollment) ensureAuthorizedKeys() error {
	sshDir := filepath.Join(e.account.Home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil { // bounded: home lu dans /etc/passwd
		return fmt.Errorf("créer %s : %w", sshDir, err)
	}
	if err := os.Chmod(sshDir, 0o700); err != nil { // #nosec G302 -- un dossier .ssh, pas un fichier : 0700 est le mode qu'exige sshd
		return fmt.Errorf("fermer %s : %w", sshDir, err)
	}
	if err := e.deps.Chown(sshDir, e.account.UID, e.account.GID); err != nil {
		return fmt.Errorf("donner %s à %s : %w", sshDir, AccountName, err)
	}

	target := filepath.Join(sshDir, "authorized_keys")
	current, found, err := readIfExists(target)
	if err != nil {
		return err
	}
	if found && bytes.Equal(current, []byte(e.publicKey)) {
		e.steps.unchanged("clé autorisée dans %s", target)
		return nil
	}

	if err := writeSystemFile(sshDir, "authorized_keys", []byte(e.publicKey), 0o600); err != nil {
		return err
	}
	if err := e.deps.Chown(target, e.account.UID, e.account.GID); err != nil {
		return fmt.Errorf("donner %s à %s : %w", target, AccountName, err)
	}

	written, _, err := readIfExists(target)
	if err != nil {
		return err
	}
	if !bytes.Equal(written, []byte(e.publicKey)) {
		return refusal.Refusal{
			Cause:  target + " ne porte pas la clé qu'openCloud vient d'y écrire",
			Remedy: "vérifier qui d'autre écrit ce fichier sur cette machine, puis rejouer l'amorçage",
		}
	}
	e.steps.done("clé autorisée dans %s", target)
	return nil
}

// ensureKnownHosts écrit les clés d'hôte de la machine : elles ne sont jamais
// apprises à la connexion, sinon StrictHostKeyChecking ne prouverait rien.
func (e *enrollment) ensureKnownHosts() error {
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

	if err := e.probe(ctx, client); err != nil {
		return err
	}

	reply, err := client.CheckLauncher(ctx)
	if err != nil {
		return err
	}
	if strings.Contains(reply.Output, "a password is required") {
		return refusal.Refusal{
			Cause:  "sudo demande un mot de passe au compte opencloud : la règle sudo n'a pas pris",
			Remedy: "vérifier /etc/sudoers.d/opencloud et l'ordre des règles de /etc/sudoers, puis rejouer l'amorçage",
		}
	}
	if reply.ExitCode != catalog.ExitRefused {
		return refusal.Refusal{
			Cause: fmt.Sprintf("le lanceur appelé sans identifiant répond %d au lieu de %d : %s",
				reply.ExitCode, catalog.ExitRefused, reply.Output),
			Remedy: "vérifier /usr/local/sbin/oc-launch et la règle sudo, puis rejouer l'amorçage",
		}
	}
	e.steps.note("vérifié par SSH vers %s : le lanceur répond derrière sudo", localAddress)
	return nil
}

func (e *enrollment) probe(ctx context.Context, client LocalTransport) error {
	var last error
	for attempt := 1; attempt <= probeAttempts; attempt++ {
		last = client.Probe(ctx)
		if last == nil {
			return nil
		}
		if attempt == probeAttempts {
			break
		}
		// Le rechargement de sshd ferme le port un instant.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(e.deps.ProbeWait):
		}
	}
	return refusal.Refusal{
		Cause:  fmt.Sprintf("la machine ne répond pas en SSH sur %s après %d essais : %v", localAddress, probeAttempts, last),
		Remedy: "vérifier que ssh.service écoute et que le compte opencloud accepte sa clé, puis rejouer l'amorçage",
	}
}

func (e *enrollment) markEnrolled(_ context.Context) error {
	name := path.Join(MachineDir(LocalMachineID), enrolledFileName)
	if _, found, err := readRootFile(e.deps.Root, name); err != nil {
		return err
	} else if found {
		e.steps.unchanged("marqueur d'enrôlement")
		return nil
	}

	date := e.deps.Now().UTC().Format(time.RFC3339) + "\n"
	if err := fsx.WriteFile(e.deps.Root, name, []byte(date), 0o644); err != nil {
		return fmt.Errorf("écrire %s : %w", name, err)
	}
	if err := e.ownState(name); err != nil {
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

// writeCandidate pose un fichier destiné à être validé avant d'être renommé.
func writeCandidate(name string, content []byte, mode os.FileMode) error {
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("retirer %s : %w", name, err)
	}
	if err := os.WriteFile(name, content, mode); err != nil { // bounded: chemin dérivé de la racine système
		return fmt.Errorf("écrire %s : %w", name, err)
	}
	return os.Chmod(name, mode) // bounded: chemin dérivé de la racine système
}

func restore(directory, name string, previous []byte, found bool) error {
	if !found {
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			return fmt.Errorf("retirer %s : %w", filepath.Join(directory, name), err)
		}
		return nil
	}
	return writeSystemFile(directory, name, previous, 0o644)
}
