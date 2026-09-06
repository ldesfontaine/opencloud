package enroll

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// LocalMachineID nomme la machine openCloud dans le répertoire d'état. Elle
// est enrôlée comme les autres, elle n'a pas de statut à part.
const LocalMachineID = "local"

// Le compte de service sur les machines, celui que le paquet crée ici.
const AccountName = "opencloud"

const (
	machinesDirName    = "machines"
	identityFileName   = "id_ed25519"
	publicKeyFileName  = "id_ed25519.pub"
	knownHostsFileName = "known_hosts"
	enrolledFileName   = "enrolled"

	// Le port SSH de la machine openCloud vers elle-même : celui du système,
	// openCloud n'en pose pas d'autre.
	localSSHPort = 22
	localAddress = "127.0.0.1"
	sshBinary    = "/usr/bin/ssh"
)

// MachineDir rend le dossier d'une machine, relatif au répertoire d'état.
func MachineDir(id string) string {
	return path.Join(machinesDirName, id)
}

// IdentityFile rend le chemin absolu de la clé privée : c'est ce que ssh -i
// attend, et ssh ne connaît pas notre os.Root.
func IdentityFile(root *os.Root, id string) string {
	return filepath.Join(root.Name(), MachineDir(id), identityFileName)
}

func KnownHostsFile(root *os.Root, id string) string {
	return filepath.Join(root.Name(), MachineDir(id), knownHostsFileName)
}

// LocalEndpoint : la machine openCloud se joint comme n'importe quelle
// machine, par SSH vers localhost (05-execution.md).
func LocalEndpoint(root *os.Root) transport.Endpoint {
	return transport.Endpoint{
		Address:        localAddress,
		Port:           localSSHPort,
		Account:        AccountName,
		IdentityFile:   IdentityFile(root, LocalMachineID),
		KnownHostsFile: KnownHostsFile(root, LocalMachineID),
		Binary:         sshBinary,
	}
}

// writeEnrolledMarker pose la date d'enrôlement d'une machine et dit si elle
// l'a écrite : un marqueur déjà là garde sa date, c'est celle du premier
// enrôlement.
func writeEnrolledMarker(root *os.Root, id string, at time.Time) (bool, error) {
	name := path.Join(MachineDir(id), enrolledFileName)
	if _, found, err := readRootFile(root, name); err != nil {
		return false, err
	} else if found {
		return false, nil
	}

	date := at.UTC().Format(time.RFC3339) + "\n"
	if err := fsx.WriteFile(root, name, []byte(date), 0o644); err != nil {
		return false, fmt.Errorf("écrire %s : %w", name, err)
	}
	return true, nil
}

// Status dit si une machine est enrôlée, depuis quand, et avec quelle clé
// publique. Une machine inconnue n'est pas une erreur : elle n'est pas enrôlée.
type Status struct {
	Enrolled  bool
	Since     time.Time
	PublicKey string
}

// ReadStatus lit le marqueur et la clé publique posés par l'enrôlement.
func ReadStatus(root *os.Root, id string) (Status, error) {
	marker, err := root.ReadFile(path.Join(MachineDir(id), enrolledFileName))
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, fmt.Errorf("lire le marqueur d'enrôlement : %w", err)
	}

	since, err := time.Parse(time.RFC3339, strings.TrimSpace(string(marker)))
	if err != nil {
		return Status{}, fmt.Errorf("lire la date d'enrôlement : %w", err)
	}

	publicKey, err := root.ReadFile(path.Join(MachineDir(id), publicKeyFileName))
	if err != nil {
		return Status{}, fmt.Errorf("lire la clé publique : %w", err)
	}
	return Status{Enrolled: true, Since: since, PublicKey: strings.TrimSpace(string(publicKey))}, nil
}
