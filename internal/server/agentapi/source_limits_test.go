package agentapi

import "testing"

// Deux adresses d'un même /64 sont une seule source : un attaquant en IPv6
// ne change pas de seau en changeant d'adresse.
func TestSourceLimits_ShareOneBucketPerIPv6SixtyFour(t *testing.T) {
	limits := newBench(t).handler.sourceLimit
	for i := range perSourceBurst {
		if !limits.Allow("2001:db8:9:9::1") {
			t.Fatalf("request %d of the burst refused", i+1)
		}
	}
	if limits.Allow("2001:db8:9:9::2") {
		t.Error("a second address of the same /64 got its own bucket")
	}
	if !limits.Allow("2001:db8:9:a::1") {
		t.Error("another /64 must have its own bucket")
	}
}
