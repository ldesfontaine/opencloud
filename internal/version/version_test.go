package version

import "testing"

func TestNumber_StaysFrozenAtV001(t *testing.T) {
	if got := Number(); got != "v0.0.1" {
		t.Fatalf("version %q : la version reste v0.0.1 tant que Lucas n'a pas tranché", got)
	}
}
