package enroll

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// account est ce que /etc/passwd dit du compte de service. Le shell en fait
// partie : c'est lui qui dit si l'étape « compte » a déjà eu lieu.
type account struct {
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
}

// Un /etc/passwd tient largement là-dedans ; au-delà, ce n'est pas un
// /etc/passwd.
const maxPasswdBytes = 1 << 20

var (
	errAccountNotFound = fmt.Errorf("compte absent de /etc/passwd")
	errGroupNotFound   = fmt.Errorf("groupe absent de /etc/group")
)

// lookupAccount lit /etc/passwd plutôt que os/user : il donne le shell, dont
// l'idempotence a besoin, et il se lit dans une racine de test.
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
		return account{Name: name, UID: uid, GID: gid, Home: fields[5], Shell: fields[6]}, nil
	}
	return account{}, errAccountNotFound
}

// passwordLocked lit la deuxième colonne de « passwd -S » : L ou LK quand le
// mot de passe est verrouillé, P quand il est utilisable, NP quand il n'y en a pas.
func passwordLocked(status string) bool {
	fields := strings.Fields(status)
	if len(fields) < 2 {
		return false
	}
	return fields[1] == "L" || fields[1] == "LK"
}

// groupHasMember lit /etc/group : le groupe existe-t-il, et le compte y est-il
// déjà ? Lu dans la racine de test comme /etc/passwd.
func groupHasMember(groupPath, group, member string) (bool, error) {
	content, err := os.ReadFile(groupPath) // #nosec G304 -- bounded: racine système + constante etc/group
	if err != nil {
		return false, fmt.Errorf("lire %s : %w", groupPath, err)
	}
	if len(content) > maxPasswdBytes {
		return false, fmt.Errorf("%s fait plus de %d octets", groupPath, maxPasswdBytes)
	}

	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 4 || fields[0] != group {
			continue
		}
		for _, current := range strings.Split(fields[3], ",") {
			if current == member {
				return true, nil
			}
		}
		return false, nil
	}
	return false, errGroupNotFound
}
