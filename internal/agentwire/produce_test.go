package agentwire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Les étiquettes JSON de l'ordre sont figées par les agents installés : un
// champ renommé ne serait plus lu.
func TestBackupProduceParams_JSONLabelsAreFrozen(t *testing.T) {
	encoded, err := json.Marshal(BackupProduceParams{Producer: "proxmox-vzdump", Target: "5012", Prefix: "pve-vm-5012", Recipient: "age1abc", Parcel: "colis"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"producer":"proxmox-vzdump","target":"5012","prefix":"pve-vm-5012","recipient":"age1abc","parcel":"colis"}`
	if string(encoded) != want {
		t.Fatalf("got %s", encoded)
	}
}

// backup.produce est le huitième ordre ; un agent ne le prend que par le flux
// signé, jamais comme une observation.
func TestOrderKinds_BackupProduceIsTheEighthOrder(t *testing.T) {
	if len(OrderKinds()) != 8 || !KindBackupProduce.IsOrder() || string(KindBackupProduce) != "backup.produce" {
		t.Fatalf("kinds %v", OrderKinds())
	}
	if _, err := DecodeObservation(string(KindBackupProduce), []byte("{}")); err == nil {
		t.Error("an order was read as an observation")
	}
}

func TestProducedFileName_IsUTCAndSortsByDate(t *testing.T) {
	paris := time.FixedZone("CEST", 2*3600)
	earlier := ProducedFileName("pve-vm-5012", time.Date(2026, 10, 21, 3, 0, 0, 0, paris))
	later := ProducedFileName("pve-vm-5012", time.Date(2026, 10, 22, 3, 0, 0, 0, paris))
	if earlier != "pve-vm-5012-20261021-010000.tar.age" || !IsProducedFile(earlier) {
		t.Fatalf("got %q", earlier)
	}
	if earlier >= later {
		t.Errorf("%q must sort before %q", earlier, later)
	}
}

// Le serveur ne range dans un colis téléversé que la forme exacte : ni un
// chemin, ni une clé déposée, ni un fichier en cours d'écriture.
func TestIsProducedFile_OnlyTheExactForm(t *testing.T) {
	for name, want := range map[string]bool{
		"pve-vm-5012-20261021-030000.tar.age":      true,
		"opencloud-20261021-030000.tar.age":        true,
		"a1-20261021-030000.tar.age":               true,
		"opencloud-key-0123456789abcdef.age":       false,
		"pve-vm-5012-20261021-030000.tar.age.part": false,
		"PVE-20261021-030000.tar.age":              false,
		"-pve-20261021-030000.tar.age":             false,
		"pve-20261021-03000.tar.age":               false,
		"pve-20261021-030000.tar":                  false,
		"../pve-20261021-030000.tar.age":           false,
		"a/b-20261021-030000.tar.age":              false,
		"pve-20261021-030000.tar.age\n":            false,
		"":                                         false,
	} {
		if got := IsProducedFile(name); got != want {
			t.Errorf("IsProducedFile(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestIsProducePrefix_OnlyLowercaseDigitsAndDashes(t *testing.T) {
	for prefix, want := range map[string]bool{
		"pve-vm-5012":                 true,
		"opencloud":                   true,
		"a":                           true,
		strings.Repeat("a", 64):       true,
		strings.Repeat("a", 65):       false,
		"":                            false,
		"-pve":                        false,
		"Pve":                         false,
		"pve_vm":                      false,
		"pve vm":                      false,
		"pve/vm":                      false,
		"../pve":                      false,
		"pve\n":                       false,
		"pve-vm-5012-20261021-030000": true,
	} {
		if got := IsProducePrefix(prefix); got != want {
			t.Errorf("IsProducePrefix(%q) = %t, want %t", prefix, got, want)
		}
	}
}
