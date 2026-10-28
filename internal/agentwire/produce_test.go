package agentwire

import (
	"encoding/json"
	"testing"
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
	if len(OrderKinds()) != 12 || !KindBackupProduce.IsOrder() || string(KindBackupProduce) != "backup.produce" {
		t.Fatalf("kinds %v", OrderKinds())
	}
	if _, err := DecodeObservation(string(KindBackupProduce), []byte("{}")); err == nil {
		t.Error("an order was read as an observation")
	}
}
