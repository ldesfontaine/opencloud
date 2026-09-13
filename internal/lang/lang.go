package lang

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed *.toml
var files embed.FS

// Code d'une langue, tel qu'il apparaît dans <html lang> et dans le réglage.
type Code string

const (
	French  Code = "fr"
	English Code = "en"
	Default      = French
)

// Les langues servies, dans l'ordre d'affichage du commutateur.
func Codes() []Code {
	return []Code{French, English}
}

func Parse(value string) (Code, bool) {
	for _, code := range Codes() {
		if string(code) == value {
			return code, true
		}
	}
	return "", false
}

// Catalog est le dictionnaire d'une langue : clé pointée → chaîne.
type Catalog struct {
	code    Code
	strings map[string]string
}

func (c Catalog) Code() Code {
	return c.code
}

// Get renvoie la chaîne, ou la clé entre crochets si elle manque : une page
// qui affiche [clé] se repère à l'œil, et le test des clés l'attrape avant.
func (c Catalog) Get(key string) string {
	if value, ok := c.strings[key]; ok {
		return value
	}
	return "[" + key + "]"
}

// Format applique fmt.Sprintf à la chaîne de la clé.
func (c Catalog) Format(key string, args ...any) string {
	return fmt.Sprintf(c.Get(key), args...)
}

func (c Catalog) Keys() []string {
	keys := make([]string, 0, len(c.strings))
	for key := range c.strings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Catalogs porte toutes les langues chargées.
type Catalogs map[Code]Catalog

// Load lit chaque fichier embarqué ; une langue absente ou mal formée est une
// erreur au démarrage, pas à l'affichage.
func Load() (Catalogs, error) {
	catalogs := make(Catalogs, len(Codes()))
	for _, code := range Codes() {
		content, err := files.ReadFile(string(code) + ".toml")
		if err != nil {
			return nil, fmt.Errorf("read language %s: %w", code, err)
		}
		strings, err := parse(content)
		if err != nil {
			return nil, fmt.Errorf("parse language %s: %w", code, err)
		}
		catalogs[code] = Catalog{code: code, strings: strings}
	}
	return catalogs, nil
}

// For renvoie la langue demandée, ou la langue par défaut si elle est inconnue.
func (c Catalogs) For(code Code) Catalog {
	if catalog, ok := c[code]; ok {
		return catalog
	}
	return c[Default]
}

// Les fichiers sont des tables TOML imbriquées ; les clés s'aplatissent avec
// des points : [nav] machines = "…" devient nav.machines.
func parse(content []byte) (map[string]string, error) {
	var tree map[string]any
	if err := toml.NewDecoder(bytes.NewReader(content)).Decode(&tree); err != nil {
		return nil, err
	}
	flat := make(map[string]string)
	if err := flatten("", tree, flat); err != nil {
		return nil, err
	}
	return flat, nil
}

func flatten(prefix string, tree map[string]any, into map[string]string) error {
	for name, value := range tree {
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) == "" {
				return fmt.Errorf("key %s is empty", key)
			}
			into[key] = typed
		case map[string]any:
			if err := flatten(key, typed, into); err != nil {
				return err
			}
		default:
			return fmt.Errorf("key %s: want a string, got %T", key, value)
		}
	}
	return nil
}
