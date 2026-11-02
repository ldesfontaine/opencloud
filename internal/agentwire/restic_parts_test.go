package agentwire

import "testing"

func TestDatabaseEngine_ReadsTheImageNameOnly(t *testing.T) {
	cases := map[string]string{
		"postgres":                         EnginePostgres,
		"postgres:16-alpine":               EnginePostgres,
		"docker.io/library/postgres:16":    EnginePostgres,
		"ghcr.io/x/postgis@sha256:abc":     EnginePostgres,
		"mariadb:11":                       EngineMariaDB,
		"mysql:8.4":                        EngineMySQL,
		"registry.example:5000/mongo:7":    EngineMongo,
		"my-postgres-ui:1":                 "",
		"redis:7":                          "",
		"nginx":                            "",
		"":                                 "",
		"registry.example:5000/app/pg:1.2": "",
	}
	for image, want := range cases {
		if got := DatabaseEngine(image); got != want {
			t.Errorf("DatabaseEngine(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestResticBackupParams_ServiceNeedsSomethingToSave(t *testing.T) {
	base := ResticBackupParams{Target: "m-abc", Scope: ResticScopeService}
	if base.Validate() == nil {
		t.Fatal("a service with no folder, volume or database must be refused")
	}
	for label, params := range map[string]ResticBackupParams{
		"folder":   {Target: "m-abc", Scope: ResticScopeService, Service: "nextcloud"},
		"volume":   {Target: "m-abc", Scope: ResticScopeService, Volumes: []string{"nextcloud_data"}},
		"database": {Target: "m-abc", Scope: ResticScopeService, Databases: []string{"nextcloud-db-1"}},
		"all":      {Target: "m-abc", Scope: ResticScopeService, Service: "nextcloud", Volumes: []string{"a"}, Databases: []string{"b"}},
	} {
		if err := params.Validate(); err != nil {
			t.Errorf("%s: %v", label, err)
		}
	}
	for _, name := range []string{"../etc", "a/b", "a b", "a:b", "-x", "", "é"} {
		params := ResticBackupParams{Target: "m-abc", Scope: ResticScopeService, Volumes: []string{name}}
		if params.Validate() == nil {
			t.Errorf("volume %q must be refused", name)
		}
	}
}

func TestResticBackupParams_VMRefusesServiceParts(t *testing.T) {
	params := ResticBackupParams{Target: "m-abc", Scope: ResticScopeVM, VMID: 100, GuestType: GuestQEMU, Volumes: []string{"x"}}
	if params.Validate() == nil {
		t.Error("a guest backup carries no volume")
	}
}

func TestResticPartReport_IsBoundedByTheClosedLists(t *testing.T) {
	good := ResticPartReport{Kind: ResticPartVolume, Name: "data", State: ResticPartFailed, Reason: ReasonDumpFailed, BytesAdded: 3}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for label, bad := range map[string]ResticPartReport{
		"kind":        {Kind: "disk", Name: "x", State: ResticPartOK},
		"state":       {Kind: ResticPartVolume, Name: "x", State: "weird"},
		"free reason": {Kind: ResticPartVolume, Name: "x", State: ResticPartFailed, Reason: "the password is hunter2"},
		"folder name": {Kind: ResticPartFolder, Name: "x", State: ResticPartOK},
		"path name":   {Kind: ResticPartVolume, Name: "/etc/passwd", State: ResticPartOK},
		"negative":    {Kind: ResticPartVolume, Name: "x", State: ResticPartOK, BytesAdded: -1},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s must be refused", label)
		}
	}
}
