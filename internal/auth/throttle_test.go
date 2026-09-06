package auth

import (
	"fmt"
	"testing"
	"time"
)

func TestThrottle_PerUsername_BlocksAfterMaxFailuresUntilWindowPasses(t *testing.T) {
	throttle := newLoginThrottle()
	now := time.Now()

	for attempt := 0; attempt < maxLoginFailures; attempt++ {
		if !throttle.allow("admin", now) {
			t.Fatalf("tentative %d : doit passer", attempt)
		}
		throttle.recordFailure("admin", now)
	}

	if throttle.allow("admin", now) {
		t.Fatal("après cinq échecs, admin doit attendre")
	}
	if !throttle.allow("someone-else", now) {
		t.Fatal("un autre identifiant n'est pas bloqué")
	}
	if !throttle.allow("admin", now.Add(loginFailureWindow+time.Second)) {
		t.Fatal("la fenêtre passée, admin peut réessayer")
	}
}

func TestThrottle_GlobalCap_BlocksEveryoneUntilWindowPasses(t *testing.T) {
	throttle := newLoginThrottle()
	now := time.Now()

	for attempt := 0; attempt < maxLoginAttemptsPerWindow; attempt++ {
		username := fmt.Sprintf("guess-%d", attempt)
		if !throttle.allow(username, now) {
			t.Fatalf("tentative %d : doit passer", attempt)
		}
		throttle.recordFailure(username, now)
	}

	if throttle.allow("admin", now) {
		t.Fatal("le plafond global atteint, même l'opérateur attend")
	}
	if !throttle.allow("admin", now.Add(loginFailureWindow+time.Second)) {
		t.Fatal("la fenêtre passée, tout le monde peut réessayer")
	}
}

func TestThrottle_ExpiredFailures_AreForgotten(t *testing.T) {
	throttle := newLoginThrottle()
	now := time.Now()
	for attempt := 0; attempt < 50; attempt++ {
		throttle.recordFailure(fmt.Sprintf("guess-%d", attempt), now)
	}

	throttle.allow("admin", now.Add(loginFailureWindow+time.Second))

	if len(throttle.failures) != 0 {
		t.Fatalf("la carte doit être vide après la fenêtre, contient %d identifiants", len(throttle.failures))
	}
}

func TestThrottle_TooManyUsernames_FailsClosed(t *testing.T) {
	throttle := newLoginThrottle()
	now := time.Now()
	for count := 0; count < maxTrackedUsernames; count++ {
		throttle.failures[fmt.Sprintf("guess-%d", count)] = []time.Time{now}
	}

	if throttle.allow("newcomer", now) {
		t.Fatal("carte pleine : un identifiant inconnu est refusé plutôt qu'alloué")
	}
	if !throttle.allow("guess-1", now) {
		t.Fatal("un identifiant déjà suivi, sous le plafond d'échecs, passe")
	}
}
