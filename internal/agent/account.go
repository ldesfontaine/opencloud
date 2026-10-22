package agent

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/registry"
)

// rootHome est le dossier personnel de root, où Docker garde ses identifiants
// quand l'agent tourne en root.
const rootHome = "/root"

// lookupUser est os/user.LookupId, que les tests remplacent : le compte du
// processus ne se choisit pas dans un test.
type lookupUser func(uid string) (*user.User, error)

// CurrentAccount rend le compte sous lequel l'agent tourne, tel que le serveur
// le lira. Un compte sans nom lisible, ou dont le nom ne passe pas dans un
// en-tête (majuscules, point), est dit par son uid seul.
func CurrentAccount() machine.Account {
	return accountOf(os.Geteuid(), user.LookupId)
}

func accountOf(euid int, lookup lookupUser) machine.Account {
	account := machine.Account{UID: int64(euid)}
	found, err := lookup(strconv.Itoa(euid))
	if err == nil && machine.IsAccountName(found.Username) {
		account.Name = found.Username
	}
	return account
}

// AccountLabel nomme un compte pour une phrase ou une commande : son nom, ou
// son uid décimal (chown accepte les deux).
func AccountLabel(account machine.Account) string {
	if account.Name != "" {
		return account.Name
	}
	return strconv.FormatInt(account.UID, 10)
}

// homeDir rend le dossier personnel du compte de l'agent, d'un seul calcul
// pour le registre et pour Compose : « /root » pour root ; sinon $HOME s'il est
// absolu, sinon celui que la base des comptes donne, s'il est absolu. Vide
// sinon : rien n'est lu, rien n'est deviné.
func homeDir() string {
	return homeDirOf(os.Geteuid(), os.Getenv("HOME"), user.LookupId)
}

func homeDirOf(euid int, environment string, lookup lookupUser) string {
	if euid == 0 {
		return rootHome
	}
	if filepath.IsAbs(environment) {
		return environment
	}
	found, err := lookup(strconv.Itoa(euid))
	if err == nil && filepath.IsAbs(found.HomeDir) {
		return found.HomeDir
	}
	return ""
}

// dockerConfigPath est le config.json de Docker du compte de l'agent.
func dockerConfigPath() string {
	return registry.ConfigPath(homeDir())
}
