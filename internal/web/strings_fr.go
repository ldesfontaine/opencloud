package web

import (
	"errors"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

// Tout ce que l'opérateur lit venant du code, en français. Les gabarits
// portent leurs propres libellés.
const (
	messageInvalidLogin        = "Identifiant ou mot de passe incorrect."
	messageFormExpired         = "Le formulaire a expiré. Rechargez la page et recommencez."
	messageTooManyAttempts     = "Trop de tentatives. Réessayez dans quelques minutes."
	messageConfirmationDiffers = "Les deux mots de passe saisis ne correspondent pas."
	messageServerError         = "Une erreur interne s'est produite. Le journal en dit plus."
)

// Chaque refus de auth au changement de mot de passe, et sa phrase.
var passwordRefusalMessages = map[error]string{
	auth.ErrInvalidCredentials: messageInvalidLogin,
	auth.ErrPasswordEmpty:      "Le nouveau mot de passe est vide.",
	auth.ErrPasswordIsDefault:  "Le nouveau mot de passe ne peut pas être celui par défaut.",
	auth.ErrPasswordUnchanged:  "Le nouveau mot de passe est identique à l'actuel.",
}

// messageForPasswordRefusal traduit un refus de auth ; une erreur qui n'est
// pas un refus rend false et reste une erreur.
func messageForPasswordRefusal(err error) (string, bool) {
	for refusal, message := range passwordRefusalMessages {
		if errors.Is(err, refusal) {
			return message, true
		}
	}
	return "", false
}
