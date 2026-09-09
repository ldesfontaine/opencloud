package auth

import (
	"net/netip"
	"sync"
	"time"
)

// Limitation des tentatives (OWASP, Authentication Cheat Sheet), à trois
// niveaux, vérifiés dans cet ordre :
//
//   - par adresse d'origine : au-delà de maxFailuresPerAddress échecs dans
//     loginFailureWindow, cette adresse seule attend. C'est le frein qui
//     arrête vraiment celui qui martèle, sans fermer la porte aux autres ;
//   - par identifiant : au-delà de maxLoginFailures échecs dans la même
//     fenêtre, la connexion est refusée jusqu'à ce qu'elle glisse ;
//   - global : au-delà de maxLoginAttemptsPerWindow tentatives dans la même
//     fenêtre, tous identifiants confondus, tout le monde attend. Ça ferme la
//     porte en cas d'attaque plutôt que de laisser un inconnu occuper le
//     processeur en argon2id.
//
// Les compteurs vivent en mémoire : un redémarrage les remet à zéro,
// acceptable pour un seul opérateur. Contrepartie assumée : qui connaît
// l'identifiant peut le bloquer cinq minutes, et une attaque distribuée finit
// par atteindre le plafond global, qui reste le filet.
const (
	maxLoginFailures          = 5
	loginFailureWindow        = 5 * time.Minute
	maxLoginAttemptsPerWindow = 100
	// Plafond de la carte par identifiant. Le plafond global l'empêche déjà
	// de grossir plus vite que cent entrées par fenêtre ; celui-ci est le
	// filet si ce raisonnement se trompe : au-delà, on refuse plutôt que
	// d'allouer.
	maxTrackedUsernames = 1000

	// Vingt échecs par adresse sur la même fenêtre : de quoi se tromper
	// plusieurs fois depuis le bureau, pas de quoi essayer un dictionnaire.
	maxFailuresPerAddress = 20
	// Plafond de la carte par adresse : une attaque distribuée ne doit pas
	// faire grossir la mémoire sans fin. Carte pleine, une adresse inconnue
	// n'est plus suivie et retombe sur les deux autres freins — la refuser
	// fermerait la porte à l'opérateur, ce que ce frein cherche justement à
	// éviter.
	maxTrackedAddresses = 10000
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

// addressThrottle compte les échecs par adresse d'origine. Même fenêtre
// glissante que le frein par identifiant.
type addressThrottle struct {
	mutex    sync.Mutex
	failures map[netip.Addr][]time.Time
}

func newAddressThrottle() *addressThrottle {
	return &addressThrottle{failures: map[netip.Addr][]time.Time{}}
}

// allow dit si une adresse peut encore tenter sa chance ; sinon, dans combien
// de temps elle le pourra, pour que le refus le dise.
func (t *addressThrottle) allow(address netip.Addr, now time.Time) (allowed bool, retryIn time.Duration) {
	// Adresse d'origine inconnue : rien à compter, les deux autres freins
	// restent.
	if !address.IsValid() {
		return true, 0
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.forgetExpiredFailures(now)
	recentFailures := t.failures[address]
	if len(recentFailures) < maxFailuresPerAddress {
		return true, 0
	}
	// L'adresse repasse sous le plafond quand cet échec-là sort de la
	// fenêtre : le dernier de trop, pas forcément le plus vieux.
	lastTooMany := recentFailures[len(recentFailures)-maxFailuresPerAddress]
	return false, loginFailureWindow - now.Sub(lastTooMany)
}

func (t *addressThrottle) recordFailure(address netip.Addr, now time.Time) {
	if !address.IsValid() {
		return
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()

	failedAt, tracked := t.failures[address]
	if !tracked && len(t.failures) >= maxTrackedAddresses {
		return
	}
	t.failures[address] = append(keepRecent(failedAt, now), now)
}

func (t *addressThrottle) reset(address netip.Addr) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	delete(t.failures, address)
}

// forgetExpiredFailures purge la carte : une adresse sans échec récent en
// sort. À appeler mutex tenu.
func (t *addressThrottle) forgetExpiredFailures(now time.Time) {
	for address, failedAt := range t.failures {
		recent := keepRecent(failedAt, now)
		if len(recent) == 0 {
			delete(t.failures, address)
			continue
		}
		t.failures[address] = recent
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
