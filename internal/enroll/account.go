package enroll

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// account est ce que /etc/passwd dit du compte de service : de quoi donner au
// compte ce qu'openCloud écrit sous son répertoire d'état.
type account struct {
	Name string
	UID  int
	GID  int
	Home string
}

// Un /etc/passwd tient largement là-dedans ; au-delà, ce n'est pas un
// /etc/passwd.
const maxPasswdBytes = 1 << 20

var errAccountNotFound = fmt.Errorf("compte absent de /etc/passwd")

// lookupAccount lit /etc/passwd plutôt que os/user : il se lit dans une racine
// de test, et il dit tout du compte en une fois.
func lookupAccount(passwdPath, name string) (account, error) {
	content, err := os.ReadFile(passwdPath) // #nosec G304 -- bounded: racine système + constante etc/passwd
	if err != nil {
		return account{}, fmt.Errorf("lire %s : %w", passwdPath, err)
	}
	if len(content) > maxPasswdBytes {
		return account{}, fmt.Errorf("%s fait plus de %d octets", passwdPath, maxPasswdBytes)
	}

	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 || fields[0] != name {
			continue
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			return account{}, fmt.Errorf("uid illisible pour %s : %w", name, err)
		}
		gid, err := strconv.Atoi(fields[3])
		if err != nil {
			return account{}, fmt.Errorf("gid illisible pour %s : %w", name, err)
		}
		return account{Name: name, UID: uid, GID: gid, Home: fields[5]}, nil
	}
	return account{}, errAccountNotFound
}
