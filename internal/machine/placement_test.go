package machine

import (
	"context"
	"errors"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/hostinfo"
)

func TestKindOfVirt(t *testing.T) {
	cases := map[string]Kind{
		"": KindUnknown, "none": KindPhysical, "kvm": KindVM, "qemu": KindVM, "vmware": KindVM, "microsoft": KindVM,
		"lxc": KindLXC, "lxc-libvirt": KindLXC, "openvz": KindLXC,
		"docker": KindUnknown, "podman": KindUnknown, "wsl": KindUnknown,
	}
	for virt, want := range cases {
		if got := KindOfVirt(virt); got != want {
			t.Errorf("%q : got %q, want %q", virt, got, want)
		}
	}
}

func TestEffectiveKind_TheStrongestSourceWins(t *testing.T) {
	cases := map[string]struct {
		placement Placement
		want      Kind
	}{
		"la correction d'abord":          {Placement{Override: KindPhysical, GuestKind: KindVM, Reported: KindVM}, KindPhysical},
		"l'hôte avant l'agent":           {Placement{GuestKind: KindLXC, Reported: KindUnknown, HasHost: true}, KindLXC},
		"VM avec hôte":                   {Placement{Reported: KindVM, HasHost: true}, KindVM},
		"VM sans hôte, adresse publique": {Placement{Reported: KindVM, Address: "203.0.113.7:51234"}, KindVPS},
		"VM sans hôte, adresse privée":   {Placement{Reported: KindVM, Address: "10.10.80.12:51234"}, KindVM},
		"VM sans hôte, adresse inconnue": {Placement{Reported: KindVM}, KindVM},
		"VM sans hôte, adresse partagée": {Placement{Reported: KindVM, Address: "100.64.1.2"}, KindVM},
		"VM sans hôte, ULA":              {Placement{Reported: KindVM, Address: "[fd12:3456::5]:443"}, KindVM},
		"VM sans hôte, IPv6 publique":    {Placement{Reported: KindVM, Address: "[2001:db8::5]:443"}, KindVPS},
		"l'opérateur dit VPS":            {Placement{Reported: KindVM, Override: KindVPS, Address: "10.0.0.5"}, KindVPS},
		"physique":                       {Placement{Reported: KindPhysical}, KindPhysical},
		"agent ancien":                   {Placement{}, KindUnknown},
		"conteneur sans hôte":            {Placement{Reported: KindLXC}, KindLXC},
		"invité de type inconnu":         {Placement{GuestKind: KindUnknown, Reported: KindVM, Address: "198.51.100.4:9"}, KindVPS},
	}
	for name, test := range cases {
		if got := EffectiveKind(test.placement); got != test.want {
			t.Errorf("%s : got %q, want %q", name, got, test.want)
		}
	}
}

func TestParseKind(t *testing.T) {
	if kind, err := ParseKind(""); err != nil || kind != "" {
		t.Fatalf("vide : %q %v", kind, err)
	}
	if kind, err := ParseKind("vps"); err != nil || kind != KindVPS {
		t.Fatalf("vps : %q %v", kind, err)
	}
	if _, err := ParseKind("mainframe"); !errors.Is(err, ErrKindUnknown) {
		t.Fatalf("inconnu : %v", err)
	}
}

func TestSetPlacement_PinsTheKindAndTheHost(t *testing.T) {
	h := newHarness(t)
	web, _ := h.enroll(t, "web")
	pve, _ := h.enroll(t, "pve")
	updated, err := h.service.SetPlacement(context.Background(), web.ID, KindVPS, pve.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.KindOverride != KindVPS || updated.HostOverride != pve.ID {
		t.Fatalf("got %+v", updated)
	}
	cleared, err := h.service.SetPlacement(context.Background(), web.ID, "", "")
	if err != nil || cleared.KindOverride != "" || cleared.HostOverride != "" {
		t.Fatalf("retirer : %+v %v", cleared, err)
	}
}

func TestSetPlacement_RefusesWhatCannotBeAHost(t *testing.T) {
	h := newHarness(t)
	a, _ := h.enroll(t, "a")
	b, _ := h.enroll(t, "b")
	c, _ := h.enroll(t, "c")
	if _, err := h.service.SetPlacement(context.Background(), a.ID, "", b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.SetPlacement(context.Background(), b.ID, "", c.ID); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		machine, host string
		kind          Kind
		want          error
	}{
		"elle-même":                 {a.ID, a.ID, "", ErrHostInvalid},
		"un hôte inconnu":           {a.ID, "nope", "", ErrHostInvalid},
		"une boucle directe":        {b.ID, a.ID, "", ErrHostInvalid},
		"une boucle par deux hôtes": {c.ID, a.ID, "", ErrHostInvalid},
		"un type inconnu":           {a.ID, "", "mainframe", ErrKindUnknown},
		"une machine inconnue":      {"nope", "", "", ErrNotFound},
	}
	for name, test := range cases {
		if _, err := h.service.SetPlacement(context.Background(), test.machine, test.kind, test.host); !errors.Is(err, test.want) {
			t.Errorf("%s : %v", name, err)
		}
	}
}

func TestRecordHost_KeepsTheKindTheAgentReports(t *testing.T) {
	h := newHarness(t)
	web, _ := h.enroll(t, "web")
	if err := h.service.RecordHost(context.Background(), web.ID, hostinfo.Report{Virt: "kvm"}); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.store.GetMachine(context.Background(), web.ID)
	if stored.Kind != KindVM {
		t.Fatalf("kind %q", stored.Kind)
	}
}

func TestRecordHost_RefusesAReportOutOfRange(t *testing.T) {
	h := newHarness(t)
	web, _ := h.enroll(t, "web")
	err := h.service.RecordHost(context.Background(), web.ID, hostinfo.Report{Virt: "<script>"})
	if !errors.Is(err, hostinfo.ErrReportInvalid) {
		t.Fatalf("got %v", err)
	}
}
