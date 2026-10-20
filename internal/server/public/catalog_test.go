package public

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/status"
)

func TestStatusPublic_CatalogServesOnlyThePageKeysInItsLanguage(t *testing.T) {
	server := newBench(t)
	if _, err := server.status.SetPage(context.Background(), status.Page{Language: "en"}); err != nil {
		t.Fatal(err)
	}
	recorder := server.get("/statut/api/i18n")
	var strings map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &strings); err != nil {
		t.Fatal(err)
	}
	if len(strings) == 0 || strings["status.state_down"] != "Outage" {
		t.Fatalf("catalog %v", strings)
	}
	for key := range strings {
		if !hasStatusPrefix(key) {
			t.Fatalf("key %q is not a public one", key)
		}
	}
}
