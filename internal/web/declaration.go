package web

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

// Les champs des formulaires de la machine.
const (
	nameFieldName        = "name"
	addressFieldName     = "address"
	portFieldName        = "port"
	fingerprintFieldName = "fingerprint"
)

// Le compte de service que la commande d'enrôlement pose sur la machine. Il
// est fixé : l'opérateur ne le choisit pas (15-catalogue-actions.md).
const machineAccount = "opencloud"

// validate.Port commence à 1024 parce qu'un service servi par le proxy n'écoute
// jamais plus bas. L'accès d'une machine, lui, est du SSH : le port par défaut
// est 22, donc on accepte ici tout port assignable.
const (
	minSSHPort     = 1
	maxSSHPort     = 65535
	defaultSSHPort = 22
)

// L'empreinte d'une clé d'hôte telle que ssh-keygen l'affiche : SHA256 en
// base64 sans remplissage, donc 43 caractères.
var fingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// Les lettres accentuées du français, dépliées : « Serveur Été » donne
// « serveur-ete ».
var accentFolding = map[rune]rune{
	'à': 'a', 'â': 'a', 'ä': 'a', 'ç': 'c', 'é': 'e', 'è': 'e', 'ê': 'e',
	'ë': 'e', 'î': 'i', 'ï': 'i', 'ô': 'o', 'ö': 'o', 'ù': 'u', 'û': 'u',
	'ü': 'u', 'ÿ': 'y', 'ñ': 'n',
}

// declaredMachine lit le formulaire de déclaration et rend la machine à
// insérer, ou le refus qui dit pourquoi elle ne l'est pas.
func declaredMachine(r *http.Request) (store.Machine, *refusalView) {
	name := strings.TrimSpace(r.PostFormValue(nameFieldName))
	id := identifierOf(name)
	if validate.Slug(id) != nil {
		refused := refusalNameWithoutIdentifier(name)
		return store.Machine{}, &refused
	}
	if id == store.LocalMachineID {
		refused := refusalIdentifierReserved(id)
		return store.Machine{}, &refused
	}

	address, port, refused := declaredAccess(r)
	if refused != nil {
		return store.Machine{}, refused
	}
	return store.Machine{
		ID:      id,
		Name:    name,
		Address: address,
		Port:    port,
		Account: machineAccount,
	}, nil
}

// declaredAccess lit l'adresse et le port : la déclaration et le changement
// d'accès posent les mêmes deux questions.
func declaredAccess(r *http.Request) (string, int, *refusalView) {
	typed := strings.TrimSpace(r.PostFormValue(addressFieldName))
	address, err := validate.Address(typed)
	if err != nil {
		// Une machine se joint aussi par son nom d'hôte, pas seulement par IP.
		if validate.Domain(typed) != nil {
			refused := refusalAddressUnknown(typed)
			return "", 0, &refused
		}
		address = typed
	}

	typedPort := strings.TrimSpace(r.PostFormValue(portFieldName))
	port, err := strconv.Atoi(typedPort)
	if err != nil || port < minSSHPort || port > maxSSHPort {
		refused := refusalPortOutOfRange(typedPort)
		return "", 0, &refused
	}
	return address, port, nil
}

// identifierOf dérive l'identifiant du nom : minuscules, accents dépliés, un
// tiret pour tout le reste. Les chemins et les unités portent cet identifiant,
// il ne se saisit donc jamais à la main (15-catalogue-actions.md §4).
func identifierOf(name string) string {
	var letters strings.Builder
	for _, letter := range strings.ToLower(name) {
		if folded, accented := accentFolding[letter]; accented {
			letter = folded
		}
		if letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' {
			letters.WriteRune(letter)
			continue
		}
		letters.WriteByte('-')
	}
	return joinWithSingleDashes(letters.String())
}

// joinWithSingleDashes retire les tirets des bords et réduit les suites à un
// seul : « web  1 » ne doit pas donner « web--1 ».
func joinWithSingleDashes(value string) string {
	var parts []string
	for _, part := range strings.Split(value, "-") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "-")
}

func validFingerprint(value string) bool {
	return fingerprintPattern.MatchString(value)
}
