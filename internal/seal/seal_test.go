package seal

import (
	"errors"
	"strings"
	"testing"
)

var passwordScope = Scope{Table: "settings", Column: "mail_password", Row: "1"}

func TestSealOpen_RoundTrips(t *testing.T) {
	keyring := Generate()
	for _, plaintext := range []string{"hunter2", "https://discord.example/api/webhooks/1/abc", "é€ ü 日本", strings.Repeat("x", 5000), ""} {
		sealed := keyring.Seal(plaintext, passwordScope)
		got, err := keyring.Open(sealed, passwordScope)
		if err != nil {
			t.Fatalf("open %q: %v", plaintext, err)
		}
		if got != plaintext {
			t.Fatalf("got %q, want %q", got, plaintext)
		}
		if plaintext != "" && strings.Contains(sealed, plaintext) {
			t.Fatalf("sealed value contains the plaintext: %q", sealed)
		}
	}
}

func TestSeal_NeverRepeatsForTheSamePlaintext(t *testing.T) {
	keyring := Generate()
	first := keyring.Seal("same", passwordScope)
	second := keyring.Seal("same", passwordScope)
	if first == second {
		t.Fatal("two seals of the same plaintext are identical: the nonce is not drawn each time")
	}
}

func TestSeal_Format(t *testing.T) {
	keyring := Generate()
	sealed := keyring.Seal("x", passwordScope)
	if !strings.HasPrefix(sealed, "v1:"+keyring.ID()+":") {
		t.Fatalf("unexpected format: %q", sealed)
	}
	if len(keyring.ID()) != 8 {
		t.Fatalf("key id %q is not 8 characters", keyring.ID())
	}
}

func TestOpen_WrongKeyIsUnknownKey(t *testing.T) {
	sealed := Generate().Seal("secret", passwordScope)
	if _, err := Generate().Open(sealed, passwordScope); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("got %v, want ErrUnknownKey", err)
	}
}

func TestOpen_ModifiedByteIsTampered(t *testing.T) {
	keyring := Generate()
	sealed := keyring.Seal("secret", passwordScope)
	// Un caractère du milieu : le dernier ne porte que des bits de remplissage,
	// que le décodage strict refuse avant même GCM.
	middle := len(sealed) - 6
	replacement := byte('A')
	if sealed[middle] == 'A' {
		replacement = 'B'
	}
	altered := sealed[:middle] + string(replacement) + sealed[middle+1:]
	if _, err := keyring.Open(altered, passwordScope); !errors.Is(err, ErrTampered) {
		t.Fatalf("got %v, want ErrTampered", err)
	}
}

func TestOpen_OtherRowOrColumnIsTampered(t *testing.T) {
	keyring := Generate()
	sealed := keyring.Seal("secret", Scope{Table: "alert_channels", Column: "url", Row: "1"})
	for name, other := range map[string]Scope{
		"other row":    {Table: "alert_channels", Column: "url", Row: "2"},
		"other column": {Table: "alert_channels", Column: "secret", Row: "1"},
		"other table":  {Table: "heartbeats", Column: "url", Row: "1"},
	} {
		if _, err := keyring.Open(sealed, other); !errors.Is(err, ErrTampered) {
			t.Errorf("%s: got %v, want ErrTampered", name, err)
		}
	}
}

func TestOpen_RefusesWhatIsNotSealed(t *testing.T) {
	keyring := Generate()
	for _, value := range []string{"", "hunter2", "v1:", "v1:zzzzzzzz:AAAA", "v2:" + keyring.ID() + ":AAAA"} {
		if _, err := keyring.Open(value, passwordScope); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: got %v, want ErrMalformed", value, err)
		}
	}
}

func TestIsSealed(t *testing.T) {
	keyring := Generate()
	sealed := keyring.Seal("x", passwordScope)
	if !IsSealed(sealed) {
		t.Fatalf("a sealed value is not recognised: %q", sealed)
	}
	// Même vide, un chiffré porte nonce et étiquette : 28 octets.
	if !IsSealed(keyring.Seal("", passwordScope)) {
		t.Fatal("a sealed empty value is not recognised")
	}
	body := sealed[len("v1:"+keyring.ID()+":"):]
	for name, value := range map[string]string{
		"empty":        "",
		"plain":        "smtp-password",
		"a plain url":  "https://hooks.example/abc",
		"too short":    "v1:" + keyring.ID() + ":AAAA",
		"not base64":   "v1:" + keyring.ID() + ":" + strings.Repeat("!", 40),
		"extra part":   sealed + ":x",
		"uppercase id": "v1:ABCDEF12:" + body,
		"short id":     "v1:abcdef:" + body,
	} {
		if IsSealed(value) {
			t.Errorf("%s: %q is taken for a sealed value", name, value)
		}
	}
}
