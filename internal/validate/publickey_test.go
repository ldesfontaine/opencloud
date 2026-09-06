package validate

import (
	"errors"
	"strings"
	"testing"
)

// Une vraie clé ed25519, produite par ssh-keygen puis figée : le test prouve
// que la forme acceptée est celle qu'un opérateur colle.
const exampleKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIHpTGTicxb0BKIIYvrttVb6FXVlzylA31bLB7qqcdax"

func TestPublicKey_AcceptsAnEd25519KeyWithOrWithoutComment(t *testing.T) {
	cases := map[string]string{
		"sans commentaire":      exampleKey,
		"avec commentaire":      exampleKey + " opencloud@machine",
		"commentaire maximal":   exampleKey + " " + strings.Repeat("c", MaxPublicKeyCommentLength),
		"espaces surnuméraires": "  " + exampleKey + "  ",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := PublicKey(value); err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
		})
	}
}

func TestPublicKey_RefusesEverythingElse(t *testing.T) {
	body := strings.Fields(exampleKey)[1]
	cases := map[string]string{
		"vide":                    "",
		"type seul":               PublicKeyType,
		"autre type":              "ssh-rsa " + body,
		"options authorized_keys": "no-pty " + exampleKey,
		"deux clés sur une ligne": exampleKey + "\n" + exampleKey,
		"base64 invalide":         PublicKeyType + " AAAA!!!",
		"blob trop court":         PublicKeyType + " " + body[:len(body)-4],
		"commentaire trop long":   exampleKey + " " + strings.Repeat("c", MaxPublicKeyCommentLength+1),
		"commentaire de contrôle": exampleKey + " machine\x07",
		"clé privée":              "-----BEGIN OPENSSH PRIVATE KEY-----",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := PublicKey(value); !errors.Is(err, ErrInvalidPublicKey) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}

func TestPublicKey_RefusesABlobThatNamesAnotherType(t *testing.T) {
	// Le bon nombre d'octets, mais le blob dit ssh-rsa : la base64 et le
	// contenu doivent dire la même chose.
	forged := "ssh-ed25519 AAAAB3NzaC1yc2EAAAAIIHpTGTicxb0BKIIYvrttVb6FXVlzylA31bLB7qqcdax"
	if err := PublicKey(forged); !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("un blob qui nomme un autre type doit être refusé, reçu %v", err)
	}
}
