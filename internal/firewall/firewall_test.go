package firewall_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/firewall"
)

func validDefinition() firewall.Definition {
	return firewall.Definition{
		Name: "Bordure", Kind: firewall.KindOPNsense, APIURL: "https://fw.lan:8443",
		APIKey: "key-of-the-api-pair", APISecret: "secret-of-the-api-pair",
	}
}

func TestNormalize_PutsTheInputInCanonicalForm(t *testing.T) {
	definition := validDefinition()
	definition.Name = "  Bordure  "
	definition.APIURL = " https://fw.lan:8443/ "
	definition.Pin = "sha256 Fingerprint=AB:CD:" + strings.Repeat("EF:", 28) + "01:23"
	normalized, err := definition.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Name != "Bordure" || normalized.APIURL != "https://fw.lan:8443" {
		t.Errorf("name and address: %+v", normalized)
	}
	if len(normalized.Pin) != 64 || normalized.Pin != strings.ToLower(normalized.Pin) || strings.Contains(normalized.Pin, ":") {
		t.Errorf("pin %q", normalized.Pin)
	}
}

func TestNormalize_RefusesWhatIsWrongWithItsKey(t *testing.T) {
	cases := map[string]struct {
		change func(*firewall.Definition)
		want   error
	}{
		"empty name":      {func(d *firewall.Definition) { d.Name = "  " }, firewall.ErrNameInvalid},
		"control in name": {func(d *firewall.Definition) { d.Name = "a\nb" }, firewall.ErrNameInvalid},
		"long name":       {func(d *firewall.Definition) { d.Name = strings.Repeat("é", 81) }, firewall.ErrNameInvalid},
		"unknown kind":    {func(d *firewall.Definition) { d.Kind = "pfsense" }, firewall.ErrKindInvalid},
		"http":            {func(d *firewall.Definition) { d.APIURL = "http://fw.lan" }, firewall.ErrURLInvalid},
		"path":            {func(d *firewall.Definition) { d.APIURL = "https://fw.lan/api" }, firewall.ErrURLInvalid},
		"userinfo":        {func(d *firewall.Definition) { d.APIURL = "https://a:b@fw.lan" }, firewall.ErrURLInvalid},
		"short key":       {func(d *firewall.Definition) { d.APIKey = "short" }, firewall.ErrKeyInvalid},
		"space in key":    {func(d *firewall.Definition) { d.APIKey = "a key with spaces" }, firewall.ErrKeyInvalid},
		"empty secret":    {func(d *firewall.Definition) { d.APISecret = "" }, firewall.ErrSecretInvalid},
		"bad pin":         {func(d *firewall.Definition) { d.Pin = "not-hex" }, firewall.ErrPinInvalid},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			definition := validDefinition()
			test.change(&definition)
			if _, err := definition.Normalize(); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}
