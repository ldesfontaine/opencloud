package enroll

import (
	"context"
	"fmt"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

const (
	keyscanPath = "/usr/bin/ssh-keyscan"
	// Les trois algorithmes qu'un sshd de Debian ou d'Ubuntu sert ; tout ce
	// qu'il présente est écrit dans known_hosts, pas seulement l'ed25519.
	keyscanTypes = "ed25519,rsa,ecdsa"
	// Assez pour une machine lente, assez peu pour ne pas figer l'écran.
	keyscanTimeoutSeconds = 10

	// Les bornes d'un port TCP. Celles de validate.Port ne valent pas ici :
	// une machine écoute très souvent sur 22.
	minSSHPort = 1
	maxSSHPort = 65535
)

// RemoteTransport est ce que la confirmation attend du transport : joindre la
// machine, y poser le lanceur, et prouver qu'il répond derrière sudo.
type RemoteTransport interface {
	Probe(ctx context.Context) error
	InstallLauncher(ctx context.Context, binary []byte) error
	CheckLauncher(ctx context.Context) (transport.LauncherReply, error)
}

// ConfirmDeps rassemble ce que la confirmation touche en dehors d'elle-même.
// Les tests y mettent un dossier temporaire, un faux exécuteur et un faux SSH.
type ConfirmDeps struct {
	Root      *os.Root
	Commands  CommandRunner
	Transport func(transport.Endpoint) RemoteTransport
	Launcher  func() ([]byte, error)
	Now       func() time.Time
	ProbeWait time.Duration
}

// SystemConfirmDeps est le câblage de production : les vrais binaires, le vrai
// client SSH, le lanceur posé à côté de l'exécutable courant.
func SystemConfirmDeps(root *os.Root) ConfirmDeps {
	return ConfirmDeps{
		Root:      root,
		Commands:  SystemCommands{},
		Transport: func(endpoint transport.Endpoint) RemoteTransport { return transport.NewSSH(endpoint) },
		Launcher:  LauncherBytes,
		Now:       time.Now,
		ProbeWait: probeInterval,
	}
}

// Confirm reçoit l'empreinte que l'opérateur a lue sur la machine, relève les
// clés d'hôte, n'écrit known_hosts que si l'une d'elles correspond, puis pose
// le lanceur et le vérifie par le chemin réel.
//
// Tout échec après known_hosts laisse known_hosts en place et le marqueur
// absent : rejouer Confirm reprend là où ça s'est arrêté.
func Confirm(ctx context.Context, deps ConfirmDeps, machine store.Machine, fingerprint string) error {
	if err := writeVerifiedKnownHosts(ctx, deps, machine, fingerprint); err != nil {
		return err
	}
	if err := installRemoteLauncher(ctx, deps, machine); err != nil {
		return err
	}
	if _, err := writeEnrolledMarker(deps.Root, machine.ID, deps.Now()); err != nil {
		return err
	}
	return nil
}

// UpdateAccess relève à nouveau les clés d'hôte et réécrit known_hosts : c'est
// l'action nommée « changer adresse, port ou empreinte ». Elle ne touche ni au
// lanceur ni au marqueur : la machine est déjà enrôlée.
func UpdateAccess(ctx context.Context, deps ConfirmDeps, machine store.Machine, fingerprint string) error {
	return writeVerifiedKnownHosts(ctx, deps, machine, fingerprint)
}

// writeVerifiedKnownHosts n'écrit que ce qu'il a vérifié : rien n'est jamais
// appris à la connexion, sinon StrictHostKeyChecking ne prouverait rien.
func writeVerifiedKnownHosts(ctx context.Context, deps ConfirmDeps, machine store.Machine, fingerprint string) error {
	if !fingerprintPattern.MatchString(fingerprint) {
		return refusal.Refusal{
			Cause:  "l'empreinte saisie n'a pas la forme d'une empreinte de clé d'hôte",
			Remedy: "recopier la ligne « Saisir cette empreinte dans openCloud : SHA256:… » qu'affiche la commande d'enrôlement",
		}
	}
	if err := checkEndpoint(machine); err != nil {
		return err
	}

	keys, err := scanHostKeys(ctx, deps, machine)
	if err != nil {
		return err
	}
	if !holdsFingerprint(keys, fingerprint) {
		return refusal.Refusal{
			Cause: "l'empreinte saisie ne correspond à aucune clé d'hôte relevée : quelqu'un se fait passer " +
				"pour la machine, ou l'empreinte est mal copiée",
			Remedy: "rejouer la commande d'enrôlement sur la machine et recopier l'empreinte qu'elle affiche ; " +
				"si elle diffère encore, ne pas enrôler cette machine",
		}
	}

	name := path.Join(MachineDir(machine.ID), knownHostsFileName)
	content := knownHostsFor(keys, machine.Address, machine.Port)
	if err := fsx.WriteFile(deps.Root, name, []byte(content), 0o644); err != nil {
		return fmt.Errorf("écrire %s : %w", name, err)
	}
	return nil
}

// checkEndpoint : l'adresse et le port entrent dans un vecteur de commande.
// Ils sont bornés ici, avant, et jamais supposés.
func checkEndpoint(machine store.Machine) error {
	// Une adresse IP ou un nom d'hôte, comme l'interface l'accepte : les deux
	// formes sont étroites, et c'est l'empreinte qui prouve la machine.
	if _, err := validate.Address(machine.Address); err != nil && validate.Domain(machine.Address) != nil {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("l'adresse « %s » de cette machine n'est ni une adresse IP ni un nom d'hôte", machine.Address),
			Remedy: "corriger l'adresse de la machine depuis sa fiche",
		}
	}
	if machine.Port < minSSHPort || machine.Port > maxSSHPort {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("le port SSH « %d » de cette machine est hors des bornes d'un port TCP", machine.Port),
			Remedy: fmt.Sprintf("corriger le port de la machine : un entier entre %d et %d", minSSHPort, maxSSHPort),
		}
	}
	return nil
}

func scanHostKeys(ctx context.Context, deps ConfirmDeps, machine store.Machine) ([]hostKey, error) {
	// bounded: adresse validée par validate.Address, port borné par
	// checkEndpoint, tout le reste constant.
	result, err := deps.Commands.Run(ctx, keyscanPath,
		"-p", strconv.Itoa(machine.Port),
		"-T", strconv.Itoa(keyscanTimeoutSeconds),
		"-t", keyscanTypes,
		"--", machine.Address)
	if err != nil {
		return nil, fmt.Errorf("relever les clés d'hôte de %s : %w", machine.Address, err)
	}

	keys := parseHostKeys(result.Output)
	if len(keys) == 0 {
		cause := fmt.Sprintf("aucune clé d'hôte relevée sur %s port %d", machine.Address, machine.Port)
		// ssh-keyscan reste souvent muet quand rien ne répond : ne rien coller
		// derrière plutôt qu'un deux-points suivi de vide.
		if result.Output != "" {
			cause += " : " + result.Output
		}
		return nil, refusal.Refusal{
			Cause: cause,
			Remedy: "vérifier que la machine est allumée, que sshd y écoute sur ce port, et que rien ne filtre " +
				"entre openCloud et elle, puis rejouer la confirmation",
		}
	}
	return keys, nil
}

func holdsFingerprint(keys []hostKey, fingerprint string) bool {
	for _, key := range keys {
		if key.Fingerprint == fingerprint {
			return true
		}
	}
	return false
}

// installRemoteLauncher dépose le lanceur par SSH et le fait poser par sudo :
// il ne peut pas voyager dans la commande d'enrôlement, il fait plusieurs Mio.
func installRemoteLauncher(ctx context.Context, deps ConfirmDeps, machine store.Machine) error {
	binary, err := deps.Launcher()
	if err != nil {
		return err
	}

	client := deps.Transport(EndpointOf(deps.Root, machine))
	if err := probeWithRetries(ctx, client, machine.Address, deps.ProbeWait); err != nil {
		return err
	}
	if err := client.InstallLauncher(ctx, binary); err != nil {
		return fmt.Errorf("poser le lanceur sur %s : %w", machine.ID, err)
	}

	reply, err := client.CheckLauncher(ctx)
	if err != nil {
		return err
	}
	return checkLauncherReply(reply)
}
