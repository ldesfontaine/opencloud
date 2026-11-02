package agentwire

import (
	"strings"
	"testing"
)

func TestResticRestoreParams_AcceptOnlyAFormTheSourceCanRunSafely(t *testing.T) {
	user := "11111111-2222-4333-8444-555555555555"
	id := strings.Repeat("a", 64)
	good := ResticRestoreParams{Target: user, Snapshot: id, Path: "/srv/data/app.ini", Stamp: "20260912-140000"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	if RestoreDestination(good.Path, good.Stamp) != "/srv/data/app.ini.restaure-20260912-140000" {
		t.Fatal(RestoreDestination(good.Path, good.Stamp))
	}
	for _, mutate := range []func(p *ResticRestoreParams){
		func(p *ResticRestoreParams) { p.Target = "../x" },
		func(p *ResticRestoreParams) { p.Snapshot = "latest" },
		func(p *ResticRestoreParams) { p.Path = "/" },
		func(p *ResticRestoreParams) { p.Path = "" },
		func(p *ResticRestoreParams) { p.Path = "srv/a" },
		func(p *ResticRestoreParams) { p.Path = "/srv/../etc/shadow" },
		func(p *ResticRestoreParams) { p.Path = "/srv/a/" },
		func(p *ResticRestoreParams) { p.Path = "/srv/a\x00b" },
		func(p *ResticRestoreParams) { p.Stamp = "" },
		func(p *ResticRestoreParams) { p.Stamp = "2026-09-12" },
		func(p *ResticRestoreParams) { p.Stamp = "20260912-140000/../x" },
	} {
		bad := good
		mutate(&bad)
		if bad.Validate() == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
}
