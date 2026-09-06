package catalog

import (
	"errors"
	"testing"
)

func TestQuoteEnvValue_AlwaysQuotesAndEscapesTheFourCharactersSystemdReads(t *testing.T) {
	cases := map[string]string{
		// Le piège d'origine : sans guillemets, systemd relit « Your ».
		"Your Cloud":      `"Your Cloud"`,
		"":                `""`,
		"exemple.com":     `"exemple.com"`,
		" bordé ":         `" bordé "`,
		"tab\tici":        "\"tab\tici\"",
		"100%":            `"100%"`,
		"cent %% pour %h": `"cent %% pour %h"`,
		`guillemet"ici`:   `"guillemet\"ici"`,
		`antislash\ici`:   `"antislash\\ici"`,
		`$HOME`:           `"\$HOME"`,
		"`id`":            "\"\\`id\\`\"",
		`fin\`:            `"fin\\"`,
		"accentué é":      `"accentué é"`,
	}
	for value, expected := range cases {
		t.Run(value, func(t *testing.T) {
			quoted, err := quoteEnvValue(value)
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if quoted != expected {
				t.Fatalf("quoteEnvValue(%q) = %s, attendu %s", value, quoted, expected)
			}
		})
	}
}

func TestQuoteEnvValue_RefusesWhatSystemdWouldNotReadBack(t *testing.T) {
	cases := map[string]string{
		"saut de ligne":       "deux\nlignes",
		"retour chariot":      "deux\rlignes",
		"NUL":                 "avant\x00après",
		"marque d'ordre":      "\uFEFFvaleur",
		"UTF-8 invalide":      string([]byte{0xff, 0xfe}),
		"antislash puis saut": "continue\\\nligne",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := quoteEnvValue(value); !errors.Is(err, ErrUnwritableValue) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}

func TestRenderParamsEnv_WritesOneUppercaseLinePerParamInOrder(t *testing.T) {
	specs := []ParamSpec{
		{Name: "domaine", Label: "le domaine", Type: ParamDomain, Required: true},
		{Name: "titre", Label: "le titre", Type: ParamSlug},
		{Name: "port", Label: "le port", Type: ParamPort, Required: true},
	}
	values := map[string]string{"domaine": "exemple.com", "titre": "Your Cloud", "port": "8080"}

	rendered, err := renderParamsEnv(specs, values)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	expected := "OC_DOMAINE=\"exemple.com\"\nOC_TITRE=\"Your Cloud\"\nOC_PORT=\"8080\"\n"
	if string(rendered) != expected {
		t.Fatalf("rendu :\n%s\nattendu :\n%s", rendered, expected)
	}
}

func TestRenderParamsEnv_SkipsWhatWasNotGiven(t *testing.T) {
	specs := []ParamSpec{
		{Name: "domaine", Label: "le domaine", Type: ParamDomain, Required: true},
		{Name: "titre", Label: "le titre", Type: ParamSlug},
	}

	rendered, err := renderParamsEnv(specs, map[string]string{"domaine": "exemple.com"})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if string(rendered) != "OC_DOMAINE=\"exemple.com\"\n" {
		t.Fatalf("rendu :\n%s", rendered)
	}
}

func TestRenderParamsEnv_NoParams_IsEmpty(t *testing.T) {
	rendered, err := renderParamsEnv(nil, nil)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(rendered) != 0 {
		t.Fatalf("rendu %q, attendu vide", rendered)
	}
}
