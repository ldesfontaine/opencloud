package web

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/validate"
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

// Les libellés des actions et des machines, tels que l'opérateur les lit.
const (
	messageMachineUnknown       = "Cette machine n'existe pas."
	messageActionUnknown        = "Cette action n'existe pas."
	messageConfirmationRequired = "Cette action est irréversible ou coupe un service : cochez la confirmation avant de la lancer."
	messageFingerprintMalformed = "L'empreinte s'écrit « SHA256: » suivi de 43 caractères, telle que la commande l'a affichée sur la machine."
)

// Le statut à quatre états (02-roles.md). Aucun de ces libellés ne dit « en
// ligne » : ils disent ce qui a été constaté, et quand.
const (
	labelNotEnrolled         = "non enrôlée"
	labelActionRunning       = "action en cours"
	labelNeverProbed         = "jamais sondée"
	labelSSHFailedSince      = "SSH en échec depuis le "
	labelLauncherFailedSince = "lanceur en échec depuis le "
)

func labelReachableSince(age time.Duration) string {
	minutes := int(age.Minutes())
	if minutes < 1 {
		return "joignable, vue à l'instant"
	}
	return fmt.Sprintf("joignable, vue il y a %d min", minutes)
}

func labelLastReport(probedAt time.Time) string {
	return "dernière remontée le " + formatMoment(probedAt)
}

// L'échec porte la note du sondage quand il en a laissé une : elle dit ce que
// la machine a répondu.
func labelProbeFailed(prefix string, health MachineHealth) string {
	label := prefix + formatMoment(health.ProbedAt)
	if health.ProbeNote != "" {
		return label + " — " + health.ProbeNote
	}
	return label
}

// Les refus de la déclaration d'une machine : la cause, puis le geste qui la
// lève (17-conventions-code.md, « un refus n'est pas une erreur »).
func refusalNameWithoutIdentifier(name string) refusalView {
	return refusalView{
		Cause: fmt.Sprintf("le nom « %s » ne donne aucun identifiant utilisable", name),
		Remedy: fmt.Sprintf("donner un nom qui commence par une lettre et tient en %d caractères une fois "+
			"réduit aux minuscules, aux chiffres et aux tirets, par exemple « web-1 »", validate.MaxSlugLength),
	}
}

func refusalIdentifierReserved(id string) refusalView {
	return refusalView{
		Cause:  fmt.Sprintf("l'identifiant « %s » est celui de la machine openCloud", id),
		Remedy: "donner un autre nom à cette machine",
	}
}

func refusalIdentifierTaken(id string) refusalView {
	return refusalView{
		Cause:  fmt.Sprintf("une machine porte déjà l'identifiant « %s »", id),
		Remedy: "donner un autre nom, ou ouvrir la fiche de la machine déjà déclarée",
	}
}

func refusalAddressUnknown(address string) refusalView {
	return refusalView{
		Cause:  fmt.Sprintf("« %s » n'est ni une adresse IP ni un nom d'hôte", address),
		Remedy: "saisir une adresse IPv4 ou IPv6, ou un nom d'hôte en minuscules",
	}
}

func refusalPortOutOfRange(port string) refusalView {
	return refusalView{
		Cause:  fmt.Sprintf("« %s » n'est pas un port", port),
		Remedy: fmt.Sprintf("saisir un port entre %d et %d ; SSH écoute sur %d par défaut", minSSHPort, maxSSHPort, defaultSSHPort),
	}
}

func actionStateLabel(state store.ActionState) string {
	switch state {
	case store.StatePrepared:
		return "Préparée"
	case store.StateRunning:
		return "En cours"
	case store.StateApplied:
		return "Appliquée"
	case store.StateFailed:
		return "Échouée"
	case store.StateRefused:
		return "Refusée"
	}
	return string(state)
}

func scopeLabel(scope catalog.Scope) string {
	switch scope {
	case catalog.ScopeInfrastructure:
		return "l'infrastructure"
	case catalog.ScopeMachine:
		return "la machine"
	case catalog.ScopeEnvironment:
		return "un environnement"
	case catalog.ScopeService:
		return "un service"
	case catalog.ScopeDomain:
		return "un domaine"
	}
	return string(scope)
}

func placeLabel(place catalog.Place) string {
	switch place {
	case catalog.PlaceTarget:
		return "sur la machine cible"
	case catalog.PlaceOpenCloud:
		return "sur la machine openCloud"
	case catalog.PlaceThirdParty:
		return "chez un tiers (DNS)"
	}
	return string(place)
}

func reversibilityLabel(reversible bool) string {
	if reversible {
		return "réversible"
	}
	return "irréversible"
}

func interruptionLabel(interrupts bool) string {
	if interrupts {
		return "oui, elle coupe un service en marche"
	}
	return "non"
}

func exitCodeLabel(code int) string {
	switch code {
	case catalog.ExitDone:
		return "0 — fait"
	case catalog.ExitFailed:
		return "1 — échoué"
	case catalog.ExitRefused:
		return "2 — refusé"
	}
	return strconv.Itoa(code)
}

// Le geste qui lève un défaut d'enrôlement dépend de la machine : openCloud
// s'enrôle sur elle-même, les autres reçoivent une commande générée.
func enrolmentLabel(machine store.Machine, status EnrolmentStatus) string {
	if status.Enrolled {
		return "enrôlée depuis le " + formatMoment(status.Since)
	}
	if machine.ID == store.LocalMachineID {
		return "non enrôlée : jouer « sudo opencloud enroll-local » sur la machine"
	}
	return "non enrôlée : aucune action ne peut partir vers cette machine"
}
