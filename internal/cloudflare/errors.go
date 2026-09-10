package cloudflare

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	// ErrInvalidToken : Cloudflare refuse le jeton, ou le dit hors service.
	ErrInvalidToken = errors.New("cloudflare refused the token")
	// ErrZoneNotFound : aucune zone de ce nom sur le compte du jeton.
	ErrZoneNotFound = errors.New("cloudflare zone not found")
	// ErrUnreadableReply : la réponse n'est pas celle de l'API v4. Un portail
	// captif ou un proxy qui répond à sa place, jamais Cloudflare.
	ErrUnreadableReply = errors.New("cloudflare reply is not valid JSON")
)

// APIError : ce que Cloudflare a répondu quand il a dit non. Les phrases sont
// les siennes, en anglais ; l'appelant les met en français s'il les affiche.
type APIError struct {
	Status   int
	Messages []string
}

// newAPIError garde les phrases de errors[] et jette le reste. Un corps sans
// message laisse le code HTTP parler seul.
func newAPIError(status int, reported []apiMessage) *APIError {
	failure := &APIError{Status: status}
	for _, message := range reported {
		if message.Message == "" {
			continue
		}
		failure.Messages = append(failure.Messages, message.Message)
	}
	return failure
}

func (e *APIError) Error() string {
	if len(e.Messages) == 0 {
		return fmt.Sprintf("cloudflare refused the call (HTTP %d)", e.Status)
	}
	return fmt.Sprintf("cloudflare refused the call (HTTP %d): %s", e.Status, strings.Join(e.Messages, " ; "))
}

// Unauthorized : le jeton est en cause, pas l'appel. 403 vaut ici 401 —
// Cloudflare rend l'un ou l'autre selon l'endpoint.
func (e *APIError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}
