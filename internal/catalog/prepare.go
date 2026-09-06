package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/scripts"
	"github.com/ldesfontaine/opencloud/internal/validate"
)

// scriptDigestPrefix : la même écriture que le journal de transaction et que
// l'interface montrent, « sha256:<hex> ».
const scriptDigestPrefix = "sha256:"

var (
	// ErrUnknownKind : l'action demandée n'est pas au catalogue. Ce n'est pas
	// un refus : l'opérateur ne choisit que dans le catalogue.
	ErrUnknownKind = errors.New("unknown action kind")
	// ErrUnknownParamType : une définition déclare un type que validate ne
	// connaît pas. Une faute de code, jamais une saisie.
	ErrUnknownParamType = errors.New("unknown parameter type")
)

// Prepare valide les paramètres et rend tout ce que l'action dépose. Un
// paramètre inconnu, manquant ou hors forme est un refus nommé, avant tout
// effet.
func Prepare(kind Kind, params map[string]string) (Prepared, error) {
	definition, found := Lookup(kind)
	if !found {
		return Prepared{}, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}

	validated, err := validateParams(definition, params)
	if err != nil {
		return Prepared{}, err
	}
	paramsEnv, err := renderParamsEnv(definition.Params, validated)
	if err != nil {
		return Prepared{}, err
	}
	script, err := scripts.Script(string(kind))
	if err != nil {
		return Prepared{}, fmt.Errorf("assemble script: %w", err)
	}

	digest := sha256.Sum256(script)
	return Prepared{
		Definition:   definition,
		Params:       validated,
		Script:       script,
		ScriptDigest: scriptDigestPrefix + hex.EncodeToString(digest[:]),
		ParamsEnv:    paramsEnv,
	}, nil
}

func validateParams(definition Definition, params map[string]string) (map[string]string, error) {
	if err := refuseUnknownParams(definition, params); err != nil {
		return nil, err
	}

	validated := make(map[string]string, len(definition.Params))
	for _, spec := range definition.Params {
		value := params[spec.Name]
		if value == "" {
			if spec.Required {
				return nil, refusal.Refusal{
					Cause:  fmt.Sprintf("le paramètre « %s » de l'action « %s » n'est pas renseigné", spec.Label, definition.Label),
					Remedy: fmt.Sprintf("donner %s, puis relancer l'action", spec.Label),
				}
			}
			continue
		}
		normalized, err := checkParam(spec, value)
		if err != nil {
			return nil, err
		}
		validated[spec.Name] = normalized
	}
	return validated, nil
}

func refuseUnknownParams(definition Definition, params map[string]string) error {
	var unknown []string
	for name := range params {
		if !slices.ContainsFunc(definition.Params, func(spec ParamSpec) bool { return spec.Name == name }) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}

	// Trié : le même appel donne toujours le même refus, mot pour mot.
	slices.Sort(unknown)
	return refusal.Refusal{
		Cause:  fmt.Sprintf("l'action « %s » ne prend pas de paramètre « %s »", definition.Label, strings.Join(unknown, " », « ")),
		Remedy: "retirer ce que l'action ne demande pas : l'écran de l'action montre ce qu'elle attend",
	}
}

// checkParam vérifie une valeur selon son type et rend sa forme normalisée.
func checkParam(spec ParamSpec, value string) (string, error) {
	normalized := value
	var expected string
	var err error

	switch spec.Type {
	case ParamDomain:
		expected = "un nom de domaine en minuscules, comme « exemple.com »"
		err = validate.Domain(value)
	case ParamPort:
		expected = fmt.Sprintf("un port entre %d et %d", validate.MinPort, validate.MaxPort)
		_, err = validate.Port(value)
	case ParamSlug:
		expected = "un nom en minuscules commençant par une lettre, comme « nextcloud »"
		err = validate.Slug(value)
	case ParamAccount:
		expected = "un nom de compte système en minuscules, comme « opencloud »"
		err = validate.Account(value)
	case ParamSlot:
		expected = "un nom de slot en minuscules, sans point"
		err = validate.Slot(value)
	case ParamDigest:
		expected = "une empreinte d'image « sha256:… », jamais un tag"
		err = validate.Digest(value)
	case ParamAddress:
		expected = "une adresse IP, comme « 192.168.1.10 »"
		normalized, err = validate.Address(value)
	case ParamPublicKey:
		expected = "une clé publique ssh-ed25519, telle que ssh-keygen l'écrit"
		err = validate.PublicKey(value)
	default:
		return "", fmt.Errorf("%w: %s", ErrUnknownParamType, spec.Type)
	}

	if err != nil {
		return "", refusal.Refusal{
			Cause:  fmt.Sprintf("le paramètre « %s » n'a pas la forme attendue", spec.Label),
			Remedy: "donner " + expected,
		}
	}
	return normalized, nil
}
