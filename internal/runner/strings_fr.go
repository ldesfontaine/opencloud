package runner

import (
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Les notes du journal, lues telles quelles par l'opérateur.
const messageScriptChanged = "refusée à la reprise : le script a changé entre-temps, " +
	"ce n'est plus l'action qui avait été préparée. Relancez-la depuis la fiche de la machine."

// La machine openCloud s'enrôle sur elle-même ; les autres reçoivent une
// commande générée (elle viendra avec l'enrôlement à distance).
func messageNotEnrolled(machine store.Machine) string {
	remedy := fmt.Sprintf("enrôler la machine %s avant de lui envoyer une action", machine.Name)
	if machine.ID == store.LocalMachineID {
		remedy = "jouer « sudo opencloud enroll-local » sur la machine openCloud"
	}
	return refusal.Refusal{
		Cause:  fmt.Sprintf("la machine %s n'est pas enrôlée : rien ne peut y être déposé", machine.Name),
		Remedy: remedy,
	}.Error()
}

func messageNoTransport(machine store.Machine, err error) string {
	return fmt.Sprintf("aucun accès à la machine %s : %v", machine.Name, err)
}

func messageDepositFailed(err error) string {
	return fmt.Sprintf("le dépôt des fichiers de l'action a échoué : %v", err)
}

func messageLaunchFailed(err error) string {
	return fmt.Sprintf("le lancement de l'unité a échoué : %v", err)
}

func messageFollowFailed(err error) string {
	return fmt.Sprintf("la lecture du journal de la machine a échoué : %v", err)
}

func messageTimedOut(action store.Action) string {
	return fmt.Sprintf("délai maximum dépassé (%d s) : la machine a arrêté l'action elle-même.",
		action.TimeoutSeconds)
}

func messageKilled(action store.Action) string {
	return fmt.Sprintf("l'action a été arrêtée par un signal ; aller voir : journalctl -u %s", action.UnitName)
}

func messageUnexpectedExit(code int) string {
	return fmt.Sprintf("code de retour inattendu (%d) : un script sort 0, 1 ou 2.", code)
}

func messagePrepareFailed(err error) string {
	return fmt.Sprintf("la préparation de l'action a échoué à la reprise : %v", err)
}

// L'action continue peut-être là-bas : on le dit, et on donne la commande.
func messageFollowAbandoned(action store.Action, unreachableFor time.Duration) string {
	return fmt.Sprintf("suivi abandonné, machine injoignable depuis %s ; aller voir : journalctl -u %s",
		frenchMinutes(unreachableFor), action.UnitName)
}

// L'unité n'a rien conclu avant le délai maximum plus la marge : elle est
// morte (RuntimeMaxSec) ou son journal n'est pas lisible. On ne devine pas.
func messageFollowDeadline(action store.Action) string {
	return fmt.Sprintf("suivi abandonné, aucune conclusion de l'unité avant le délai maximum (%d s) plus la marge ; "+
		"aller voir : journalctl -u %s", action.TimeoutSeconds, action.UnitName)
}

func frenchMinutes(elapsed time.Duration) string {
	minutes := int(elapsed.Minutes())
	if minutes < 1 {
		return "moins d'une minute"
	}
	return fmt.Sprintf("%d min", minutes)
}
