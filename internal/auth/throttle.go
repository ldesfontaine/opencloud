package auth

import (
	"sync"
	"time"
)

// Limitation des tentatives, par identifiant : au-delà de maxLoginFailures
// échecs dans loginFailureWindow, la connexion est refusée jusqu'à ce que la
// fenêtre glisse (OWASP, Authentication Cheat Sheet). Le compteur vit en
// mémoire : un redémarrage le remet à zéro, acceptable pour un seul opérateur.
// Contrepartie assumée : qui connaît l'identifiant peut le bloquer cinq
// minutes ; l'adresse d'origine viendra avec le proxy (X-Forwarded-For).
const (
	maxLoginFailures   = 5
	loginFailureWindow = 5 * time.Minute
)

type loginThrottle struct {
	mutex    sync.Mutex
	failures map[string][]time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{failures: map[string][]time.Time{}}
}

func (t *loginThrottle) isBlocked(username string, now time.Time) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	recent := t.recentFailures(username, now)
	t.failures[username] = recent
	return len(recent) >= maxLoginFailures
}

func (t *loginThrottle) recordFailure(username string, now time.Time) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.failures[username] = append(t.recentFailures(username, now), now)
}

func (t *loginThrottle) reset(username string) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	delete(t.failures, username)
}

// recentFailures ne garde que les échecs encore dans la fenêtre. À appeler
// mutex tenu.
func (t *loginThrottle) recentFailures(username string, now time.Time) []time.Time {
	var recent []time.Time
	for _, failedAt := range t.failures[username] {
		if now.Sub(failedAt) < loginFailureWindow {
			recent = append(recent, failedAt)
		}
	}
	return recent
}
