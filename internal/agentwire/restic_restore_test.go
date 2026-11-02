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

// Jamais en place : pour tout horodatage que la forme admet, la destination
// est un autre chemin que l'original (un nom de plus, sous le même dossier) ;
// et aucun horodatage vide ou truqué n'est admis pour la faire coïncider.
func TestRestoreDestination_IsNeverTheOriginal(t *testing.T) {
	user := "11111111-2222-4333-8444-555555555555"
	id := strings.Repeat("a", 64)
	for _, path := range []string{"/srv/data/app.ini", "/srv/data", "/a"} {
		for _, stamp := range []string{"20260912-140000", "00000000-000000", "99991231-235959"} {
			destination := RestoreDestination(path, stamp)
			if destination == path || !strings.HasPrefix(destination, path+".restaure-") {
				t.Errorf("%s + %s -> %s", path, stamp, destination)
			}
		}
		for _, stamp := range []string{"", ".", "..", "/", "20260912-140000/", "20260912-140000\n", " 20260912-140000"} {
			params := ResticRestoreParams{Target: user, Snapshot: id, Path: path, Stamp: stamp}
			if params.Validate() == nil {
				t.Errorf("l'horodatage %q est admis pour %s", stamp, path)
			}
		}
	}
}
