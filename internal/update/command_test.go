package update

import (
	"testing"

	"github.com/ldesfontaine/opencloud/internal/service"
)

func TestCommand_ComposeOrPullOnly(t *testing.T) {
	compose := service.Service{Image: "nextcloud:29.0.4", ComposeService: "nextcloud", ComposeDir: "/srv/nextcloud", ComposeFile: "/srv/nextcloud/compose.yaml"}
	byHand := service.Service{Image: "alpine:3.20"}
	newer := Check{Image: "nextcloud:29.0.4", Kind: KindPatch, NewerTag: "29.0.6"}
	rebuilt := Check{Image: "postgres:16.4", Kind: KindDigest}
	upToDate := Check{Image: "alpine:3.20"}

	if got := Command(compose, newer); got != "cd /srv/nextcloud && docker compose pull nextcloud && docker compose up -d nextcloud" {
		t.Fatalf("compose: %q", got)
	}
	if got := Command(compose, rebuilt); got != "cd /srv/nextcloud && docker compose pull nextcloud && docker compose up -d nextcloud" {
		t.Fatalf("compose rebuilt: %q", got)
	}
	if got := Command(byHand, Check{Image: "alpine:3.20", Kind: KindMinor, NewerTag: "3.24"}); got != "docker pull alpine:3.24" {
		t.Fatalf("by hand: %q", got)
	}
	if got := Command(byHand, Check{Image: "alpine:3.20", Kind: KindDigest}); got != "docker pull alpine:3.20" {
		t.Fatalf("by hand rebuilt: %q", got)
	}
	if got := Command(byHand, upToDate); got != "" {
		t.Fatalf("up to date must give nothing, got %q", got)
	}
	spaced := service.Service{Image: "ghcr.io/x/y:1", ComposeService: "web", ComposeDir: "/srv/mon appli"}
	if got := Command(spaced, Check{Image: "ghcr.io/x/y:1", Kind: KindMajor, NewerTag: "2"}); got != "cd '/srv/mon appli' && docker compose pull web && docker compose up -d web" {
		t.Fatalf("quoted: %q", got)
	}
}

// Le constat déduit le type ; un tag plus récent qui ne se compare pas au
// courant est effacé, et un tag flottant reconstruit se lit au digest.
func TestCheckOf_DeducesTheKind(t *testing.T) {
	same := "sha256:" + string(make([]byte, 0)) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	other := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cases := []struct {
		result   Result
		wantKind Kind
		wantTag  string
	}{
		{Result{Image: "alpine:3.20", Outcome: OutcomeOK, LocalDigest: same, RemoteDigest: same, NewerTag: "3.24"}, KindMinor, "3.24"},
		{Result{Image: "postgres:16", Outcome: OutcomeOK, LocalDigest: same, RemoteDigest: other}, KindDigest, ""},
		{Result{Image: "postgres:16", Outcome: OutcomeOK, LocalDigest: same, RemoteDigest: other, NewerTag: "17"}, KindMajor, "17"},
		{Result{Image: "alpine:3.20", Outcome: OutcomeOK, LocalDigest: same, RemoteDigest: same}, "", ""},
		{Result{Image: "alpine:3.20", Outcome: OutcomeOK, LocalDigest: same, RemoteDigest: same, NewerTag: "latest"}, "", ""},
		{Result{Image: "alpine:3.20", Outcome: OutcomeUnauthorized, LocalDigest: same}, "", ""},
	}
	for _, tc := range cases {
		got := checkOf("m", tc.result)
		if got.Kind != tc.wantKind || got.NewerTag != tc.wantTag {
			t.Errorf("%+v: kind %q tag %q, want %q %q", tc.result, got.Kind, got.NewerTag, tc.wantKind, tc.wantTag)
		}
	}
	if target := checkOf("m", cases[0].result).Target(); target != "alpine:3.24" {
		t.Fatalf("target = %q", target)
	}
}
