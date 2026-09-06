package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

func TestPrepare_Diagnostiquer_DepositsTheScriptAndItsDigest(t *testing.T) {
	prepared, err := Prepare(KindDiagnostiquer, nil)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if prepared.Definition.Kind != KindDiagnostiquer {
		t.Errorf("Definition.Kind = %q", prepared.Definition.Kind)
	}
	if !bytes.HasPrefix(prepared.Script, []byte("#!/bin/bash\n")) {
		t.Errorf("le script déposé doit commencer par le shebang : %.20q", prepared.Script)
	}
	digest := sha256.Sum256(prepared.Script)
	if expected := "sha256:" + hex.EncodeToString(digest[:]); prepared.ScriptDigest != expected {
		t.Errorf("ScriptDigest = %q, attendu %q", prepared.ScriptDigest, expected)
	}
	if len(prepared.ParamsEnv) != 0 {
		t.Errorf("ParamsEnv = %q, attendu vide : Diagnostiquer ne prend rien", prepared.ParamsEnv)
	}
	if len(prepared.Files) != 0 {
		t.Errorf("Files = %v, attendu aucun", prepared.Files)
	}
	if len(prepared.Params) != 0 {
		t.Errorf("Params = %v, attendu aucun", prepared.Params)
	}
}

func TestPrepare_UnknownKind_IsAnErrorNotARefusal(t *testing.T) {
	// Enroler n'a pas encore de définition, et l'opérateur ne choisit que dans
	// le catalogue : c'est une faute de code, pas un refus à lui montrer.
	for _, kind := range []Kind{KindEnroler, Kind("inconnue")} {
		if _, err := Prepare(kind, nil); !errors.Is(err, ErrUnknownKind) {
			t.Errorf("Prepare(%q) = %v, attendu ErrUnknownKind", kind, err)
		}
	}
}

func TestPrepare_UnknownParam_IsARefusalThatNamesIt(t *testing.T) {
	_, err := Prepare(KindDiagnostiquer, map[string]string{"domaine": "exemple.com", "port": "8080"})

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("attendu un refus, reçu %v", err)
	}
	if !strings.Contains(refused.Cause, "domaine") || !strings.Contains(refused.Cause, "port") {
		t.Fatalf("le refus doit nommer les paramètres de trop : %q", refused.Cause)
	}
	if refused.Remedy == "" {
		t.Error("un refus dit toujours le geste qui le lève")
	}
}

func TestValidateParams_MissingRequiredParam_IsARefusal(t *testing.T) {
	definition := definitionWith(ParamSpec{Name: "domaine", Label: "le domaine", Type: ParamDomain, Required: true})

	for name, params := range map[string]map[string]string{
		"absent": nil,
		"vide":   {"domaine": ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := validateParams(definition, params)

			var refused refusal.Refusal
			if !errors.As(err, &refused) {
				t.Fatalf("attendu un refus, reçu %v", err)
			}
			if !strings.Contains(refused.Cause, "le domaine") {
				t.Fatalf("le refus doit nommer le paramètre : %q", refused.Cause)
			}
		})
	}
}

func TestValidateParams_OptionalParamAbsent_Passes(t *testing.T) {
	definition := definitionWith(ParamSpec{Name: "titre", Label: "le titre", Type: ParamSlug})

	validated, err := validateParams(definition, nil)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if len(validated) != 0 {
		t.Fatalf("validated = %v, attendu vide", validated)
	}
}

func TestValidateParams_MalformedValue_IsARefusalPerType(t *testing.T) {
	cases := map[ParamType]string{
		ParamDomain:    "Exemple .com",
		ParamPort:      "443",
		ParamSlug:      "2app",
		ParamAccount:   "-svc",
		ParamSlot:      "..",
		ParamDigest:    "latest",
		ParamAddress:   "exemple.com",
		ParamPublicKey: "ssh-rsa AAAA",
	}
	for paramType, value := range cases {
		t.Run(string(paramType), func(t *testing.T) {
			definition := definitionWith(ParamSpec{Name: "valeur", Label: "la valeur", Type: paramType, Required: true})

			_, err := validateParams(definition, map[string]string{"valeur": value})

			var refused refusal.Refusal
			if !errors.As(err, &refused) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
			if refused.Remedy == "" {
				t.Error("un refus dit toujours le geste qui le lève")
			}
		})
	}
}

func TestValidateParams_WellFormedValues_AreKeptAndNormalized(t *testing.T) {
	definition := definitionWith(
		ParamSpec{Name: "domaine", Label: "le domaine", Type: ParamDomain, Required: true},
		ParamSpec{Name: "adresse", Label: "l'adresse", Type: ParamAddress, Required: true},
	)

	validated, err := validateParams(definition, map[string]string{
		"domaine": "exemple.com",
		"adresse": "2001:0DB8::0001",
	})
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if validated["domaine"] != "exemple.com" {
		t.Errorf("domaine = %q", validated["domaine"])
	}
	// L'adresse repart sous sa forme normalisée : deux écritures, une valeur.
	if validated["adresse"] != "2001:db8::1" {
		t.Errorf("adresse = %q, attendu 2001:db8::1", validated["adresse"])
	}
}

func TestCheckParam_UnknownType_IsAnError(t *testing.T) {
	spec := ParamSpec{Name: "valeur", Label: "la valeur", Type: ParamType("inventé")}

	if _, err := checkParam(spec, "peu importe"); !errors.Is(err, ErrUnknownParamType) {
		t.Fatalf("attendu ErrUnknownParamType, reçu %v", err)
	}
}

func definitionWith(params ...ParamSpec) Definition {
	return Definition{Kind: Kind("essai"), Label: "Essai", Params: params}
}
