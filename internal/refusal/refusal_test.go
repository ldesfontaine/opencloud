package refusal_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

var (
	errNameInvalid = refusal.Invalid("thing.name_invalid", "thing name must be 1 to 80 characters")
	errNameTaken   = refusal.Conflict("thing.name_taken", "thing name already used")
)

func TestKeyOf_ReadsTheKeyThroughTheWrapping(t *testing.T) {
	wrapped := fmt.Errorf("create thing: %w", errNameInvalid)
	if key, ok := refusal.KeyOf(wrapped); !ok || key != "thing.name_invalid" || refusal.IsConflict(wrapped) {
		t.Fatalf("invalid: %q %v", key, ok)
	}
	wrapped = fmt.Errorf("rename thing: %w", errNameTaken)
	if key, ok := refusal.KeyOf(wrapped); !ok || key != "thing.name_taken" || !refusal.IsConflict(wrapped) {
		t.Fatalf("conflict: %q %v", key, ok)
	}
}

func TestKeyOf_AnErrorIsNotARefusal(t *testing.T) {
	for _, err := range []error{nil, errors.New("disk full")} {
		if key, ok := refusal.KeyOf(err); ok || key != "" || refusal.IsConflict(err) {
			t.Errorf("%v: %q %v", err, key, ok)
		}
	}
}

// Une sentinelle reste une sentinelle : errors.Is la reconnaît, une autre
// de même clé ne se confond pas avec elle, et son texte est celui déclaré.
func TestError_KeepsTheSentinelBehaviour(t *testing.T) {
	if !errors.Is(fmt.Errorf("create thing: %w", errNameInvalid), errNameInvalid) {
		t.Fatal("errors.Is must see the sentinel through the wrapping")
	}
	twin := refusal.Invalid("thing.name_invalid", "thing name must be 1 to 80 characters")
	if errors.Is(twin, errNameInvalid) {
		t.Fatal("two declarations are two refusals")
	}
	if errNameInvalid.Error() != "thing name must be 1 to 80 characters" {
		t.Fatalf("message: %q", errNameInvalid.Error())
	}
}
