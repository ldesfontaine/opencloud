package scripts

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// L'en-tête commun, le corps de chaque action et, pour celles qui en ont, leur
// liste de paquets et la clé du dépôt de Docker — un dossier par action.
//
//go:embed lib.sh */run.sh */packages.txt */docker.asc
var files embed.FS

const (
	// headerName tient les réglages et les fonctions de sortie ; bodyName le
	// corps d'une action, sans shebang : c'est l'en-tête qui le porte.
	headerName = "lib.sh"
	bodyName   = "run.sh"
	// listName : la liste de paquets d'une action, quand elle en a une.
	listName = "packages.txt"
	// listVariable : le nom de la variable shell où la liste est insérée.
	listVariable = "PACKAGES"
	// keyName : la clé publique du dépôt de Docker, versionnée dans le dépôt
	// plutôt que téléchargée au moment de la pose ; un test vérifie son
	// empreinte (docker_key_test.go).
	keyName = "docker.asc"
	// keyVariable : le nom de la variable shell où la clé est insérée.
	keyVariable = "DOCKER_KEY"

	keyArmorHeader = "-----BEGIN PGP PUBLIC KEY BLOCK-----"
	keyArmorFooter = "-----END PGP PUBLIC KEY BLOCK-----"
)

// Ce qu'un nom de paquet a le droit d'être : la borne de Debian, en plus
// étroit. Elle garantit qu'aucun nom ne peut refermer les guillemets simples
// de la variable shell où la liste est insérée.
var packageName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{1,63}$`)

// Script rend le fichier tel qu'il sera déposé : l'en-tête commun, la liste de
// paquets de l'action et la clé de dépôt quand elle en a, puis le corps. Un
// seul fichier part sur la machine, il n'y a rien à y sourcer.
func Script(kind string) ([]byte, error) {
	header, err := files.ReadFile(headerName)
	if err != nil {
		return nil, fmt.Errorf("read script header: %w", err)
	}
	body, err := files.ReadFile(path.Join(kind, bodyName))
	if err != nil {
		return nil, fmt.Errorf("read script body for %q: %w", kind, err)
	}
	list, err := packageBlock(kind)
	if err != nil {
		return nil, err
	}
	key, err := dockerKeyBlock(kind)
	if err != nil {
		return nil, err
	}

	assembled := make([]byte, 0, len(header)+len(list)+len(key)+len(body))
	assembled = append(assembled, header...)
	assembled = append(assembled, list...)
	assembled = append(assembled, key...)
	return append(assembled, body...), nil
}

// DockerKey rend la clé publique du dépôt de Docker versionnée avec l'action,
// telle qu'elle sera posée sur la machine. Une action qui n'en a pas rend une
// clé vide, et ce n'est pas une erreur.
func DockerKey(kind string) (string, error) {
	content, err := files.ReadFile(path.Join(kind, keyName))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read docker key for %q: %w", kind, err)
	}
	if err := checkArmoredKey(path.Join(kind, keyName), string(content)); err != nil {
		return "", err
	}
	return string(content), nil
}

// Packages rend la liste de paquets d'une action, triée et sans doublon. Une
// action qui n'en a pas rend une liste vide, et ce n'est pas une erreur.
func Packages(kind string) ([]string, error) {
	content, err := files.ReadFile(path.Join(kind, listName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read package list for %q: %w", kind, err)
	}
	return parsePackages(kind, content)
}

// Kinds rend les actions qui ont un script, dans un ordre stable. Le catalogue
// s'y compare : un dossier sans définition est une action qu'on ne voit pas.
func Kinds() []string {
	// La racine d'une FS embarquée se lit toujours : l'erreur n'existe pas ici.
	entries, _ := files.ReadDir(".")

	kinds := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			kinds = append(kinds, entry.Name())
		}
	}
	slices.Sort(kinds)
	return kinds
}

// packageBlock rend la liste de paquets sous la forme d'une variable shell,
// insérée entre l'en-tête et le corps. C'est ainsi que la liste arrive sur la
// machine : jamais par une ligne de commande.
func packageBlock(kind string) ([]byte, error) {
	packages, err := Packages(kind)
	if err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, nil
	}

	// Guillemets simples : un nom validé n'en contient aucun, donc rien ne peut
	// refermer la chaîne ni ouvrir autre chose.
	block := "\n# Les paquets de l'action, insérés ici par Go depuis " + path.Join(kind, listName) + ".\n" +
		listVariable + "='" + strings.Join(packages, "\n") + "'\n"
	return []byte(block), nil
}

// dockerKeyBlock rend la clé du dépôt de Docker sous la forme d'une variable
// shell, insérée entre l'en-tête et le corps — la même mécanique que la liste
// de paquets. La clé arrive ainsi sur la machine, sans téléchargement au
// moment de la pose.
func dockerKeyBlock(kind string) ([]byte, error) {
	key, err := DockerKey(kind)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, nil
	}

	// Guillemets simples : checkArmoredKey a garanti que la clé n'en contient
	// aucun, donc rien ne peut refermer la chaîne ni ouvrir autre chose.
	block := "\n# La clé du dépôt de Docker, insérée ici par Go depuis " + path.Join(kind, keyName) + ".\n" +
		keyVariable + "='" + key + "'\n"
	return []byte(block), nil
}

// checkArmoredKey refuse tout ce qui n'est pas un bloc de clé publique armuré :
// les deux lignes d'armure, puis rien d'autre que l'alphabet de l'armure. Un
// guillemet simple refermerait la variable shell où la clé est insérée, et un
// caractère de contrôle n'a rien à faire dans un fichier ASCII.
func checkArmoredKey(source string, key string) error {
	if !strings.HasPrefix(key, keyArmorHeader+"\n") {
		return fmt.Errorf("read docker key %s: does not start with %s", source, keyArmorHeader)
	}
	if !strings.HasSuffix(strings.TrimRight(key, "\n"), keyArmorFooter) {
		return fmt.Errorf("read docker key %s: does not end with %s", source, keyArmorFooter)
	}
	for index, character := range key {
		if !isArmorCharacter(character) {
			return fmt.Errorf("read docker key %s: byte %d is not allowed in an armored key: %q", source, index, character)
		}
	}
	return nil
}

// L'alphabet base64, la somme de contrôle, les lignes d'armure et leurs
// séparateurs — et rien de plus.
func isArmorCharacter(character rune) bool {
	switch {
	case character >= 'a' && character <= 'z',
		character >= 'A' && character <= 'Z',
		character >= '0' && character <= '9':
		return true
	}
	return strings.ContainsRune("+/=- \n", character)
}

// parsePackages lit un paquet par ligne, ignore les commentaires et les lignes
// vides, refuse un nom qui n'en est pas un et un doublon — une liste versionnée
// se relit, elle ne se devine pas.
func parsePackages(kind string, content []byte) ([]string, error) {
	source := path.Join(kind, listName)
	var packages []string

	for number, line := range strings.Split(string(content), "\n") {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if !packageName.MatchString(name) {
			return nil, fmt.Errorf("read package list %s: line %d is not a package name: %q", source, number+1, name)
		}
		if slices.Contains(packages, name) {
			return nil, fmt.Errorf("read package list %s: line %d repeats %q", source, number+1, name)
		}
		packages = append(packages, name)
	}

	// Trié : le fichier-garde de la machine se compare à cette liste, et
	// déplacer une ligne du fichier ne doit pas ressembler à un changement.
	slices.Sort(packages)
	return packages, nil
}
