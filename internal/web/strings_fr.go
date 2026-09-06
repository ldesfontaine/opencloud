package web

import (
	"errors"
	"fmt"

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
	messageFormTooLarge        = "Le formulaire envoyé est trop volumineux."
	messageFormUnreadable      = "Le formulaire envoyé est illisible."
)

// Le refus nomme la règle et le geste qui la lève, sur un réseau de confiance.
func messagePasswordTooShort(minLength int) string {
	return fmt.Sprintf("Le nouveau mot de passe doit faire au moins %d caractères. "+
		"Sur un réseau de confiance, la clé « min_password_length » de la configuration abaisse ce minimum.", minLength)
}

// messageForPasswordRefusal traduit chaque refus de auth au changement de mot
// de passe ; une erreur qui n'est pas un refus rend false et reste une erreur.
func messageForPasswordRefusal(err error) (string, bool) {
	var tooShort *auth.PasswordTooShortError
	if errors.As(err, &tooShort) {
		return messagePasswordTooShort(tooShort.MinLength), true
	}

	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return messageInvalidLogin, true
	case errors.Is(err, auth.ErrPasswordEmpty):
		return "Le nouveau mot de passe est vide.", true
	case errors.Is(err, auth.ErrPasswordIsDefault):
		return "Le nouveau mot de passe ne peut pas être celui par défaut.", true
	case errors.Is(err, auth.ErrPasswordUnchanged):
		return "Le nouveau mot de passe est identique à l'actuel.", true
	case errors.Is(err, auth.ErrPasswordTooLong):
		return "Le nouveau mot de passe est trop long.", true
	}
	return "", false
}
