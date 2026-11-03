package agentwire

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// La liste blanche : un rapport de vérification ne porte que des comptes. Un
// agent qui y glisse un chemin, un nom de fichier ou un contenu les voit jetés
// au décodage, et ils ne repartent pas à la réécriture.
func TestResticVerifyReport_KeepsOnlyCounts(t *testing.T) {
	const seeded = "/home/alice/finances-2025.ods"
	raw := fmt.Sprintf(`{"run_id":"r1","state":"succeeded","verify":{"snapshots":2,"files":4,"bytes":4096,"path":%q,"file":%q,"content":"secret","output":"x"}}`, seeded, seeded)
	var result ActionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatal(err)
	}
	if result.Verify == nil || *result.Verify != (ResticVerifyReport{Snapshots: 2, Files: 4, Bytes: 4096}) {
		t.Fatalf("report %+v", result.Verify)
	}
	again, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(again), "alice") || strings.Contains(string(again), "secret") {
		t.Fatalf("a seeded value survived: %s", again)
	}
}

func TestResticVerifyReport_RefusesCountsOutOfBounds(t *testing.T) {
	for name, report := range map[string]ResticVerifyReport{
		"negative snapshots": {Snapshots: -1},
		"too many snapshots": {Snapshots: maxVerifySnapshots + 1},
		"too many files":     {Files: maxVerifyFiles + 1},
		"negative bytes":     {Bytes: -1},
		"too many bytes":     {Bytes: maxVerifyBytes + 1},
	} {
		if report.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if (ResticVerifyReport{Snapshots: maxVerifySnapshots, Files: maxVerifyFiles, Bytes: maxVerifyBytes}).Validate() != nil {
		t.Error("the bounds themselves are valid")
	}
}

func TestResticVerifySecret_IsMaskedAndValidated(t *testing.T) {
	secret := ResticVerifySecret{RepoPassword: strings.Repeat("a", 43)}
	if secret.Validate() != nil {
		t.Fatal("a drawn password is valid")
	}
	if (ResticVerifySecret{RepoPassword: "court"}).Validate() == nil {
		t.Error("a short password is refused")
	}
	if fmt.Sprintf("%v %+v", secret, secret) != "[secret] [secret]" {
		t.Errorf("secret printed: %v", secret)
	}
}

func TestResticVerifyParams_NeedARepositoryUser(t *testing.T) {
	if (ResticVerifyParams{User: "../x"}).Validate() == nil || (ResticVerifyParams{}).Validate() == nil {
		t.Error("a user that is not a repository name is refused")
	}
}
