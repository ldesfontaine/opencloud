package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Le contenu d'un fichier de jeton, tel que zone l'écrit : le jeton et un
// saut de ligne. Fabriqué, jamais écrit en dur : un scanner de secrets ne doit
// pas le prendre pour un vrai.
func madeUpToken(what string) string {
	return "jetable-" + what + "-pour-le-test"
}

var placedToken = madeUpToken("zone") + "\n"

type fakeTokens struct {
	tokens map[string]string
}

func (f fakeTokens) ZoneToken(zone string) ([]byte, error) {
	token, found := f.tokens[zone]
	if !found {
		return nil, refusal.Refusal{
			Cause:  "openCloud ne tient aucun jeton pour la zone « " + zone + " »",
			Remedy: "ajouter la zone et son jeton",
		}
	}
	return []byte(token), nil
}

func TestPrepare_DNSToken_DepositsTheTokenAsASecretFileIn0600(t *testing.T) {
	service := Service{Tokens: fakeTokens{tokens: map[string]string{"exemple.fr": placedToken}}}

	prepared, err := service.Prepare(KindDNSToken, map[string]string{"zone": "exemple.fr"})
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}

	if len(prepared.Files) != 1 {
		t.Fatalf("Files = %v, attendu le seul jeton", prepared.Files)
	}
	token := prepared.Files[0]
	if token.Path != "cloudflare.token" {
		t.Errorf("Path = %q", token.Path)
	}
	if token.Mode.Perm() != 0o600 {
		t.Errorf("Mode = %04o, attendu 0600", token.Mode.Perm())
	}
	if !token.Secret {
		t.Error("le fichier du jeton n'est pas marqué secret : l'écran « avant » le montrerait")
	}
	if string(token.Content) != placedToken {
		t.Errorf("le contenu déposé n'est pas celui de la zone")
	}
}

// Le jeton ne passe jamais par params.env : systemd le relirait, et il
// apparaîtrait dans l'environnement de l'unité.
func TestPrepare_DNSToken_KeepsTheTokenOutOfParamsEnv(t *testing.T) {
	service := Service{Tokens: fakeTokens{tokens: map[string]string{"exemple.fr": placedToken}}}

	prepared, err := service.Prepare(KindDNSToken, map[string]string{"zone": "exemple.fr"})
	if err != nil {
		t.Fatalf("Prepare = %v", err)
	}

	rendered := string(prepared.ParamsEnv)
	if strings.Contains(rendered, strings.TrimSpace(placedToken)) {
		t.Fatalf("params.env porte le jeton :\n%s", rendered)
	}
	if !strings.Contains(rendered, `OC_ZONE="exemple.fr"`) {
		t.Errorf("params.env ne porte pas la zone :\n%s", rendered)
	}
}

func TestPrepare_DNSToken_UnknownZone_IsANamedRefusal(t *testing.T) {
	service := Service{Tokens: fakeTokens{tokens: map[string]string{}}}

	_, err := service.Prepare(KindDNSToken, map[string]string{"zone": "inconnue.fr"})

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Prepare = %v, attendu un refus", err)
	}
	if !strings.Contains(refused.Cause, "inconnue.fr") {
		t.Errorf("le refus ne nomme pas la zone : %q", refused.Cause)
	}
}

// Sans source de jetons branchée, l'action ne se prépare pas : c'est une faute
// de câblage, pas un refus adressé à l'opérateur.
func TestPrepare_DNSToken_WithoutATokenSource_IsAnError(t *testing.T) {
	_, err := Prepare(KindDNSToken, map[string]string{"zone": "exemple.fr"})

	if !errors.Is(err, ErrNoTokens) {
		t.Fatalf("Prepare = %v, attendu ErrNoTokens", err)
	}
}

func TestPrepare_DNSToken_MissingZone_IsARefusal(t *testing.T) {
	service := Service{Tokens: fakeTokens{tokens: map[string]string{}}}

	_, err := service.Prepare(KindDNSToken, map[string]string{})

	var refused refusal.Refusal
	if !errors.As(err, &refused) {
		t.Fatalf("Prepare = %v, attendu un refus", err)
	}
}
