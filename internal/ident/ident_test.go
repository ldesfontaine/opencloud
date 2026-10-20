package ident

import (
	"regexp"
	"testing"
)

var shape = regexp.MustCompile(`^[0-9a-f]{16}$`)

// La forme est celle que l'interface et l'annuaire attendent depuis le début.
func TestNew_IsSixteenLowercaseHexAndDrawnAgainEachTime(t *testing.T) {
	first, err := New()
	if err != nil {
		t.Fatal(err)
	}
	second, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if !shape.MatchString(first) || !shape.MatchString(second) {
		t.Fatalf("shape: %q, %q", first, second)
	}
	if first == second {
		t.Fatal("two draws gave the same identifier")
	}
}
