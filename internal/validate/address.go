package validate

import (
	"errors"
	"fmt"
	"net/netip"
)

// ErrInvalidAddress : la valeur n'est pas une adresse IP.
var ErrInvalidAddress = errors.New("invalid address")

// Address rend la forme normalisée de l'adresse : deux écritures de la même
// adresse IPv6 donnent alors la même chaîne, donc la même comparaison.
func Address(value string) (string, error) {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return "", fmt.Errorf("%w: expected an IPv4 or IPv6 address", ErrInvalidAddress)
	}
	return address.String(), nil
}
