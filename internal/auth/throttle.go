package auth

import (
	"sync"
	"time"
)

// Limitation des tentatives (OWASP, Authentication Cheat Sheet), à deux
// niveaux :
//
//   - par identifiant : au-delà de maxLoginFailures échecs dans
//     loginFailureWindow, la connexion est refusée jusqu'à ce que la fenêtre
//     glisse ;
//   - global : au-delà de maxLoginAttemptsPerWindow tentatives dans la même
//     fenêtre, tous identifiants confondus, tout le monde attend. Ça ferme la
//     porte en cas d'attaque plutôt que de laisser un inconnu occuper le
//     processeur en argon2id.
//
// Les compteurs vivent en mémoire : un redémarrage les remet à zéro,
// acceptable pour un seul opérateur. Contrepartie assumée : qui connaît
// l'identifiant peut le bloquer cinq minutes, et une attaque bloque aussi
// l'opérateur ; la limitation par adresse viendra avec le proxy
// (X-Forwarded-For).
const (
	maxLoginFailures          = 5
	loginFailureWindow        = 5 * time.Minute
	maxLoginAttemptsPerWindow = 100
	// Plafond de la carte par identifiant. Le plafond global l'empêche déjà
	// de grossir plus vite que cent entrées par fenêtre ; celui-ci est le
	// filet si ce raisonnement se trompe : au-delà, on refuse plutôt que
	// d'allouer.
	maxTrackedUsernames = 1000
)

type loginThrottle struct {
	mutex    sync.Mutex
	failures map[string][]time.Time
	attempts []time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{failures: map[string][]time.Time{}}
}

// allow dit si une tentative peut commencer, et la compte si oui. À appeler
// avant tout travail coûteux.
func (t *loginThrottle) allow(username string, now time.Time) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.attempts = keepRecent(t.attempts, now)
	if len(t.attempts) >= maxLoginAttemptsPerWindow {
		return false
	}

	t.forgetExpiredFailures(now)
	recentFailures, tracked := t.failures[username]
	if len(recentFailures) >= maxLoginFailures {
		return false
	}
	if !tracked && len(t.failures) >= maxTrackedUsernames {
		return false
	}

	t.attempts = append(t.attempts, now)
	return true
}

func (t *loginThrottle) recordFailure(username string, now time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.failures[username] = append(keepRecent(t.failures[username], now), now)
}

func (t *loginThrottle) reset(username string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	delete(t.failures, username)
}

// forgetExpiredFailures purge la carte : un identifiant sans échec récent en
// sort. Mille entrées de cinq dates au plus, c'est vite parcouru. À appeler
// mutex tenu.
func (t *loginThrottle) forgetExpiredFailures(now time.Time) {
	for username, failedAt := range t.failures {
		recent := keepRecent(failedAt, now)
		if len(recent) == 0 {
			delete(t.failures, username)
			continue
		}
		t.failures[username] = recent
	}
}

// keepRecent ne garde que les dates encore dans la fenêtre.
func keepRecent(dates []time.Time, now time.Time) []time.Time {
	var recent []time.Time
	for _, date := range dates {
		if now.Sub(date) < loginFailureWindow {
			recent = append(recent, date)
		}
	}
	return recent
}
