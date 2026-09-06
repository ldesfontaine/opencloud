package validate

import (
	"errors"
	"fmt"
	"strconv"
)

// Les ports privilégiés restent à l'entrée : un service servi par le proxy
// n'en écoute jamais un.
const (
	MinPort = 1024
	MaxPort = 65535
)

// ErrInvalidPort : le port n'est pas un entier dans les bornes.
var ErrInvalidPort = errors.New("invalid port")

// Port lit un port depuis la chaîne saisie et rend l'entier validé.
func Port(value string) (int, error) {
	if !isDigitsOnly(value) || len(value) > 5 {
		return 0, fmt.Errorf("%w: expected digits only", ErrInvalidPort)
	}
	// Atoi ne peut plus échouer : au plus cinq chiffres ASCII.
	number, _ := strconv.Atoi(value)
	if number < MinPort || number > MaxPort {
		return 0, fmt.Errorf("%w: expected %d to %d", ErrInvalidPort, MinPort, MaxPort)
	}
	return number, nil
}

func isDigitsOnly(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}
