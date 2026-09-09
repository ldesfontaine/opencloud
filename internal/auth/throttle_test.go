package auth

import (
	"fmt"
	"net/netip"
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

func TestAddressThrottle_TwentyFailures_ThenTheAddressWaitsWithADelay(t *testing.T) {
	throttle := newAddressThrottle()
	address := netip.MustParseAddr("203.0.113.7")
	now := time.Now()

	for attempt := 0; attempt < maxFailuresPerAddress; attempt++ {
		if allowed, _ := throttle.allow(address, now); !allowed {
			t.Fatalf("tentative %d : doit passer", attempt)
		}
		throttle.recordFailure(address, now)
	}

	allowed, retryIn := throttle.allow(address, now)
	if allowed {
		t.Fatalf("après %d échecs, cette adresse doit attendre", maxFailuresPerAddress)
	}
	if retryIn != loginFailureWindow {
		t.Fatalf("délai = %s, attendu la fenêtre entière %s", retryIn, loginFailureWindow)
	}
	if allowed, _ := throttle.allow(netip.MustParseAddr("198.51.100.2"), now); !allowed {
		t.Fatal("une autre adresse n'est pas bloquée")
	}
}

func TestAddressThrottle_DelayShrinksAsTheWindowSlides(t *testing.T) {
	throttle := newAddressThrottle()
	address := netip.MustParseAddr("203.0.113.7")
	now := time.Now()
	for attempt := 0; attempt < maxFailuresPerAddress; attempt++ {
		throttle.recordFailure(address, now)
	}

	_, retryIn := throttle.allow(address, now.Add(time.Minute))
	if retryIn != loginFailureWindow-time.Minute {
		t.Fatalf("délai = %s, attendu %s", retryIn, loginFailureWindow-time.Minute)
	}

	if allowed, _ := throttle.allow(address, now.Add(loginFailureWindow+time.Second)); !allowed {
		t.Fatal("la fenêtre passée, l'adresse peut réessayer")
	}
	if len(throttle.failures) != 0 {
		t.Fatalf("la carte doit être vide après la fenêtre, contient %d adresses", len(throttle.failures))
	}
}

func TestAddressThrottle_Reset_ClearsTheAddress(t *testing.T) {
	throttle := newAddressThrottle()
	address := netip.MustParseAddr("203.0.113.7")
	now := time.Now()
	for attempt := 0; attempt < maxFailuresPerAddress; attempt++ {
		throttle.recordFailure(address, now)
	}

	throttle.reset(address)

	if allowed, _ := throttle.allow(address, now); !allowed {
		t.Fatal("après une réussite, le compteur de l'adresse repart de zéro")
	}
}

func TestAddressThrottle_TooManyAddresses_StopsTrackingNewOnes(t *testing.T) {
	throttle := newAddressThrottle()
	now := time.Now()
	for count := 0; count < maxTrackedAddresses; count++ {
		address := netip.AddrFrom4([4]byte{10, byte(count >> 16), byte(count >> 8), byte(count)})
		throttle.failures[address] = []time.Time{now}
	}

	newcomer := netip.MustParseAddr("203.0.113.7")
	for attempt := 0; attempt <= maxFailuresPerAddress; attempt++ {
		throttle.recordFailure(newcomer, now)
	}

	if len(throttle.failures) != maxTrackedAddresses {
		t.Fatalf("carte pleine : %d adresses suivies, attendu %d", len(throttle.failures), maxTrackedAddresses)
	}
	if allowed, _ := throttle.allow(newcomer, now); !allowed {
		t.Fatal("carte pleine, l'adresse n'est plus suivie : les deux autres freins prennent la suite")
	}
}

func TestAddressThrottle_UnreadableAddress_IsNotTracked(t *testing.T) {
	throttle := newAddressThrottle()
	now := time.Now()

	for attempt := 0; attempt <= maxFailuresPerAddress; attempt++ {
		throttle.recordFailure(netip.Addr{}, now)
	}

	if allowed, _ := throttle.allow(netip.Addr{}, now); !allowed {
		t.Fatal("sans adresse d'origine lisible, ce frein ne s'applique pas")
	}
	if len(throttle.failures) != 0 {
		t.Fatalf("rien à suivre, la carte doit rester vide, contient %d entrées", len(throttle.failures))
	}
}
