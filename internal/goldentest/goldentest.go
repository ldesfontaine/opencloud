package goldentest

import (
	"flag"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "réécrit les fichiers de référence dans testdata/")

// Compare échoue si got diffère du fichier de référence ; avec -update, il
// le réécrit d'abord.
func Compare(t testing.TB, golden, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden) // #nosec G304 -- le fichier de référence du test appelant.
	if err != nil {
		t.Fatalf("%v (relancer le test avec -update)", err)
	}
	if got != string(want) {
		t.Fatalf("sortie différente de %s :\n%s\n(relancer le test avec -update après vérification)", golden, got)
	}
}
