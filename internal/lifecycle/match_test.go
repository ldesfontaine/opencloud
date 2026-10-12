package lifecycle

import (
	"testing"
	"time"
)

var fetchedAt = time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)

func sampleSnapshot() Snapshot {
	snapshot := Snapshot{
		FetchedAt: fetchedAt,
		Repositories: map[string]string{
			"library/postgres":  "postgresql",
			"library/traefik":   "traefik",
			"library/redis":     "redis",
			"keycloak/keycloak": "keycloak",
		},
		Products: map[string]Product{
			"postgresql": {Name: "postgresql", Label: "PostgreSQL", Cycles: []Cycle{
				{Name: "18", IsMaintained: true, EOLFrom: "2030-11-14", Latest: "18.6"},
				{Name: "17", IsMaintained: true, EOLFrom: "2029-11-08", Latest: "17.11"},
				{Name: "16", IsMaintained: true, EOLFrom: "2028-11-09", Latest: "16.15"},
				{Name: "12", IsEOL: true, EOLFrom: "2024-11-21", Latest: "12.22"},
			}},
			"traefik": {Name: "traefik", Label: "Traefik", Cycles: []Cycle{
				{Name: "3.7", IsMaintained: true, Latest: "3.7.14"},
				{Name: "3.6", IsEOL: true, EOLFrom: "2026-08-16", Latest: "3.6.25"},
				{Name: "3.1", IsEOL: true, EOLFrom: "2024-10-28", Latest: "3.1.7"},
				{Name: "2.11", IsMaintained: true, EOLFrom: "2027-02-01", Latest: "2.11.30"},
			}},
		},
	}
	for name, product := range snapshot.Products {
		product.FetchedAt = fetchedAt
		snapshot.Products[name] = product
	}
	return snapshot
}

func TestVerdictOf_MaintainedBranch_SaysUntilWhenAndWhatIsAbove(t *testing.T) {
	got := verdictOf(sampleSnapshot(), "postgres:16.4", fetchedAt.Add(time.Hour))
	if got.Status != StatusMaintained || got.Product != "PostgreSQL" || got.Cycle != "16" || got.EOLFrom != "2028-11-09" || got.Latest != "16.15" {
		t.Fatalf("verdict %+v", got)
	}
	if got.ProductURL != "https://endoflife.date/postgresql" || got.IsStale || !got.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("verdict %+v", got)
	}
	if len(got.Newer) != 2 || got.Newer[0] != (Newer{Cycle: "17", Latest: "17.11", EOLFrom: "2029-11-08"}) || got.Newer[1].Cycle != "18" {
		t.Fatalf("newer %+v", got.Newer)
	}
}

func TestVerdictOf_EndOfLifeBranch_NamesOnlyMaintainedBranchesAbove(t *testing.T) {
	got := verdictOf(sampleSnapshot(), "traefik:v3.1.4", fetchedAt)
	if got.Status != StatusEOL || got.Cycle != "3.1" || got.EOLFrom != "2024-10-28" {
		t.Fatalf("verdict %+v", got)
	}
	if len(got.Newer) != 1 || got.Newer[0].Cycle != "3.7" {
		t.Fatalf("3.6 is over and 2.11 is below: %+v", got.Newer)
	}
}

func TestVerdictOf_SaysWhatItDoesNotKnow(t *testing.T) {
	cases := map[string]Status{
		"postgres:latest":                StatusUnknownVersion,
		"postgres:13.2":                  StatusUnknownVersion,
		"vaultwarden/server:1.32.0":      StatusUnknownProduct,
		"not an image":                   StatusUnknownProduct,
		"redis:7.4":                      StatusPending,
		"quay.io/keycloak/keycloak:26.0": StatusPending,
	}
	for image, want := range cases {
		if got := verdictOf(sampleSnapshot(), image, fetchedAt); got.Status != want {
			t.Errorf("%s: %s, want %s", image, got.Status, want)
		}
	}
	if got := verdictOf(Snapshot{}, "postgres:16.4", fetchedAt); got.Status != StatusPending || got.IsStale {
		t.Fatalf("nothing read yet: %+v", got)
	}
}

func TestVerdictOf_OldReading_IsMarkedStale(t *testing.T) {
	if got := verdictOf(sampleSnapshot(), "postgres:16.4", fetchedAt.Add(StaleAfter+time.Hour)); !got.IsStale || got.Status != StatusMaintained {
		t.Fatalf("verdict %+v", got)
	}
}

// Une lecture gardée vieillit : la date de fin passée vaut fin de support,
// même si la source disait encore « maintenue » le jour de la lecture.
func TestVerdictOf_EndDatePassedSinceTheReading_IsEndOfLife(t *testing.T) {
	afterPostgres16 := time.Date(2028, 11, 9, 0, 0, 0, 0, time.UTC)
	got := verdictOf(sampleSnapshot(), "postgres:16.4", afterPostgres16)
	if got.Status != StatusEOL || !got.IsStale || len(got.Newer) != 2 {
		t.Fatalf("verdict %+v", got)
	}
	afterPostgres17 := time.Date(2029, 11, 8, 12, 0, 0, 0, time.UTC)
	got = verdictOf(sampleSnapshot(), "postgres:16.4", afterPostgres17)
	if len(got.Newer) != 1 || got.Newer[0].Cycle != "18" {
		t.Fatalf("17 is over too: %+v", got.Newer)
	}
	if got := verdictOf(sampleSnapshot(), "postgres:16.4", afterPostgres16.Add(-time.Minute)); got.Status != StatusMaintained {
		t.Fatalf("the day before: %+v", got)
	}
}

func TestCycleOf_ReadsTheBranchFromTheTag(t *testing.T) {
	cycles := []Cycle{{Name: "3.20"}, {Name: "3"}, {Name: "24.04"}, {Name: "7.4"}}
	cases := map[string]string{
		"3.20.3":         "3.20",
		"v3.20":          "3.20",
		"3.19.1":         "3",
		"3":              "3",
		"24.04":          "24.04",
		"7.4-alpine":     "7.4",
		"7.4.1-bookworm": "7.4",
		"latest":         "",
		"stable":         "",
		"8.0":            "",
		"":               "",
		"20240101120000": "",
	}
	for tag, want := range cases {
		got, found := cycleOf(cycles, tag)
		if got.Name != want || found != (want != "") {
			t.Errorf("%q: %q found=%v, want %q", tag, got.Name, found, want)
		}
	}
}

func TestSystemOf_ReadsTheNameAndTheVersionTheAgentGives(t *testing.T) {
	cases := map[string][2]string{
		"Debian GNU/Linux 12":            {"debian", "12"},
		"Ubuntu 24.04":                   {"ubuntu", "24.04"},
		"Alpine Linux 3.20.3":            {"alpine-linux", "3.20.3"},
		"Rocky Linux 9.4":                {"rocky-linux", "9.4"},
		"Fedora Linux 40":                {"fedora", "40"},
		"CentOS Stream 9":                {"centos-stream", "9"},
		"Red Hat Enterprise Linux 9.4":   {"rhel", "9.4"},
		"  debian gnu/linux 12":          {"debian", "12"},
		"Debian GNU/Linux":               {"debian", ""},
		"Debian GNU/Linux trixie/sid":    {"", ""},
		"Arch Linux":                     {"", ""},
		"Raspbian GNU/Linux 11":          {"", ""},
		"linux":                          {"", ""},
		"":                               {"", ""},
		"12":                             {"", ""},
		"Windows Server 2022 Datacenter": {"", ""},
	}
	for system, want := range cases {
		product, version, known := systemOf(system)
		if product != want[0] || version != want[1] || known != (want[0] != "") {
			t.Errorf("%q: %q %q known=%v, want %v", system, product, version, known, want)
		}
	}
}

func TestVerdictOfSystem_SaysWhatItDoesNotKnow(t *testing.T) {
	snapshot := sampleSnapshot()
	snapshot.Products["debian"] = Product{Name: "debian", Label: "Debian", FetchedAt: fetchedAt, Cycles: []Cycle{{Name: "12", IsMaintained: true, EOLFrom: "2028-06-30"}}}
	snapshot.NotFound = map[string]bool{"fedora": true}
	cases := map[string]Status{
		"Debian GNU/Linux 12": StatusMaintained,
		"Debian GNU/Linux 14": StatusUnknownVersion,
		"Debian GNU/Linux":    StatusUnknownVersion,
		"Ubuntu 24.04":        StatusPending,
		"Fedora Linux 40":     StatusUnknownProduct,
		"Arch Linux":          StatusUnknownProduct,
	}
	for system, want := range cases {
		if got := verdictOfSystem(snapshot, system, fetchedAt); got.Status != want {
			t.Errorf("%q: %s, want %s", system, got.Status, want)
		}
	}
	if got := verdictOfSystem(Snapshot{}, "Debian GNU/Linux 12", fetchedAt); got.Status != StatusPending {
		t.Fatalf("nothing read yet: %+v", got)
	}
	if got := verdictOfSystem(Snapshot{}, "Arch Linux", fetchedAt); got.Status != StatusPending {
		t.Fatalf("nothing read yet, unknown system: %+v", got)
	}
}

func TestIsAbove_ComparesNumbersNotText(t *testing.T) {
	if !isAbove("3.10", "3.9") || isAbove("3.9", "3.10") || isAbove("3.9", "3.9") || !isAbove("17", "16.4") || !isAbove("3.1.1", "3.1") {
		t.Fatal("numeric comparison")
	}
	if isAbove("lts", "3") || isAbove("3", "lts") {
		t.Fatal("a name that is not a version is above nothing")
	}
}

func TestNewerMaintained_KeepsTheClosest(t *testing.T) {
	cycles := []Cycle{}
	for _, name := range []string{"25", "24", "23", "22", "21", "20", "19"} {
		cycles = append(cycles, Cycle{Name: name, IsMaintained: true})
	}
	got := newerMaintained(cycles, Cycle{Name: "19"}, "2026-10-12")
	if len(got) != maxNewer || got[0].Cycle != "20" || got[maxNewer-1].Cycle != "23" {
		t.Fatalf("newer %+v", got)
	}
}
