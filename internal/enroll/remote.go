package enroll

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

// Le nom du paramètre de l'action Enrôler : il devient OC_PUBLIC_KEY dans la
// commande collée par l'opérateur.
const publicKeyParam = "public_key"

// Un lanceur statique tient très largement là-dedans ; au-delà, ce n'est pas
// le lanceur qu'on lit.
const maxLauncherBytes = 16 << 20

// PrepareRemote crée le dossier d'une machine distante et sa paire de clés si
// elle manque, puis rend la ligne publique à poser sur la machine. Le
// processus tourne sous le compte opencloud : rien n'est donné à personne.
func PrepareRemote(root *os.Root, machineID string) (string, error) {
	// La forme des identifiants de machine est celle de migration 003, celle
	// d'une action : elle entre dans un chemin.
	if !actiondir.ValidID(machineID) {
		return "", fmt.Errorf("identifiant de machine hors forme : %q", machineID)
	}

	directory := MachineDir(machineID)
	if err := root.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("créer %s : %w", directory, err)
	}
	if err := root.Chmod(directory, 0o700); err != nil {
		return "", fmt.Errorf("fermer %s : %w", directory, err)
	}

	publicLine, err := ensureRemoteIdentity(root, machineID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(publicLine), nil
}

// ensureRemoteIdentity ne remplace jamais une clé posée : celle qui est là
// fait foi, et c'est elle qu'on republie.
func ensureRemoteIdentity(root *os.Root, machineID string) (string, error) {
	directory := MachineDir(machineID)
	privateName := path.Join(directory, identityFileName)
	publicName := path.Join(directory, publicKeyFileName)

	existing, found, err := readRootFile(root, privateName)
	if err != nil {
		return "", err
	}

	var publicLine string
	if found {
		publicLine, err = publicLineOf(existing, machineID)
		if err != nil {
			return "", err
		}
	} else {
		privateKey, line, err := generateIdentity(machineID)
		if err != nil {
			return "", err
		}
		if err := createExclusiveFile(root, privateName, privateKey); err != nil {
			return "", err
		}
		publicLine = line
	}

	current, _, err := readRootFile(root, publicName)
	if err != nil {
		return "", err
	}
	if string(current) == publicLine {
		return publicLine, nil
	}
	if err := fsx.WriteFile(root, publicName, []byte(publicLine), 0o644); err != nil {
		return "", fmt.Errorf("écrire %s : %w", publicName, err)
	}
	return publicLine, nil
}

// createExclusiveFile pose un fichier qui ne doit jamais en remplacer un autre.
func createExclusiveFile(root *os.Root, name string, content []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
	return root.Chmod(name, 0o600)
}

// Command rend la commande à coller sur la machine, en root. Elle est
// autonome : le script de l'action et la clé publique y voyagent ensemble,
// encodés, sans rien à télécharger là-bas.
func Command(publicKey string) (string, error) {
	// La clé entre entre guillemets simples : elle ne peut donc en porter
	// aucun, ni de saut de ligne qui terminerait l'export.
	if strings.ContainsAny(publicKey, "'\n\r") {
		return "", fmt.Errorf("la clé publique ne peut pas porter de guillemet ni de saut de ligne")
	}
	if err := validate.PublicKey(publicKey); err != nil {
		return "", fmt.Errorf("clé publique refusée : %w", err)
	}

	prepared, err := catalog.Prepare(catalog.KindEnroler, map[string]string{publicKeyParam: publicKey})
	if err != nil {
		return "", err
	}

	// Le shebang reste en tête : un opérateur qui décode la commande doit lire
	// un script, pas une ligne d'export. La clé se pose juste après.
	shebang, body, found := strings.Cut(string(prepared.Script), "\n")
	if !found || !strings.HasPrefix(shebang, "#!") {
		return "", fmt.Errorf("le script d'enrôlement ne commence pas par un shebang")
	}
	payload := shebang + "\nexport OC_PUBLIC_KEY='" + publicKey + "'\n" + body

	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	return "echo '" + encoded + "' | base64 -d | sudo bash", nil
}

// EndpointOf rend de quoi joindre une machine : son adresse, son port et son
// compte viennent de la base, sa clé et son known_hosts de son dossier.
func EndpointOf(root *os.Root, machine store.Machine) transport.Endpoint {
	return transport.Endpoint{
		Address:        machine.Address,
		Port:           machine.Port,
		Account:        machine.Account,
		IdentityFile:   IdentityFile(root, machine.ID),
		KnownHostsFile: KnownHostsFile(root, machine.ID),
		Binary:         sshBinary,
	}
}

// LauncherBytes lit le lanceur posé à côté de l'exécutable courant :
// /opt/opencloud/bin en production, bin/ en développement. Les deux binaires
// sont posés ensemble, ils ne divergent pas.
func LauncherBytes() ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("trouver l'exécutable courant : %w", err)
	}
	name := filepath.Join(filepath.Dir(executable), launcherName)

	info, err := os.Stat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, refusal.Refusal{
			Cause:  name + " est absent : le lanceur est posé à côté d'openCloud par le paquet",
			Remedy: "réinstaller le paquet openCloud sur la machine openCloud, puis rejouer la confirmation",
		}
	}
	if err != nil {
		return nil, fmt.Errorf("lire %s : %w", name, err)
	}
	if info.Size() > maxLauncherBytes {
		return nil, refusal.Refusal{
			Cause:  fmt.Sprintf("%s fait %d octets, plus que les %d attendus d'un lanceur", name, info.Size(), maxLauncherBytes),
			Remedy: "vérifier ce que ce fichier est devenu, puis réinstaller le paquet openCloud",
		}
	}

	content, err := os.ReadFile(name) // #nosec G304 -- bounded: dossier de l'exécutable courant, nom constant
	if err != nil {
		return nil, fmt.Errorf("lire %s : %w", name, err)
	}
	return content, nil
}
