package heartbeat_test

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

func TestHashToken_IsStableHexAndDiffersPerToken(t *testing.T) {
	first, err := heartbeat.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := heartbeat.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	hash := heartbeat.HashToken(first)
	if hash != heartbeat.HashToken(first) {
		t.Fatal("the hash of a token must not change")
	}
	if hash == heartbeat.HashToken(second) {
		t.Fatal("two tokens share a hash")
	}
	if len(hash) != 64 {
		t.Fatalf("hash %q is not 64 hexadecimal characters", hash)
	}
}
