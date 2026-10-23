package seal

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

func newRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func TestCreateLoad_RoundTripsInMode0600(t *testing.T) {
	root := newRoot(t)
	original := Generate()
	if err := Create(root, original); err != nil {
		t.Fatal(err)
	}
	info, err := root.Stat(brand.SecretKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID() != original.ID() {
		t.Fatalf("id %s, want %s", loaded.ID(), original.ID())
	}
	sealed := original.Seal("secret", passwordScope)
	if got, err := loaded.Open(sealed, passwordScope); err != nil || got != "secret" {
		t.Fatalf("the loaded key does not open what the created one sealed: %q, %v", got, err)
	}
}

func TestLoad_WithoutFileIsErrNoKey(t *testing.T) {
	if _, err := Load(newRoot(t)); !errors.Is(err, ErrNoKey) {
		t.Fatalf("got %v, want ErrNoKey", err)
	}
}

func TestLoad_RefusesAFileOthersCanRead(t *testing.T) {
	root := newRoot(t)
	if err := Create(root, Generate()); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		if err := root.Chmod(brand.SecretKeyFile, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(root); !errors.Is(err, ErrKeyFileMode) {
			t.Errorf("mode %04o: got %v, want ErrKeyFileMode", mode, err)
		}
	}
	// 0400 convient : seul le propriétaire lit.
	if err := root.Chmod(brand.SecretKeyFile, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err != nil {
		t.Fatalf("mode 0400: %v", err)
	}
}

func TestLoad_RefusesAWrongId(t *testing.T) {
	root := newRoot(t)
	keyring := Generate()
	other := Generate()
	line := keyFileScheme + ":" + other.ID() + ":" + encodeKey(keyring) + "\n"
	writeKeyFile(t, root, line)
	if _, err := Load(root); !errors.Is(err, ErrKeyFileInvalid) {
		t.Fatalf("got %v, want ErrKeyFileInvalid", err)
	}
}

func TestLoad_RefusesAnythingButKeyLines(t *testing.T) {
	root := newRoot(t)
	keyring := Generate()
	line := keyFileScheme + ":" + keyring.ID() + ":" + encodeKey(keyring)
	for name, content := range map[string]string{
		"the same key twice": line + "\n" + line + "\n",
		"a blank second":     line + "\n\n",
		"five keys":          strings.Repeat(line+"\n", 5),
		"empty":              "",
		"only a newline":     "\n",
		"unknown format":     "key-v2:" + keyring.ID() + ":" + encodeKey(keyring) + "\n",
		"short key":          keyFileScheme + ":" + keyring.ID() + ":AAAA\n",
		"not a key":          "hunter2\n",
		"too long":           strings.Repeat("x", maxKeyFileBytes+1),
	} {
		writeKeyFile(t, root, content)
		if _, err := Load(root); !errors.Is(err, ErrKeyFileInvalid) {
			t.Errorf("%s: got %v, want ErrKeyFileInvalid", name, err)
		}
	}
	// Sans saut de ligne final, la ligne reste une ligne.
	writeKeyFile(t, root, line)
	if _, err := Load(root); err != nil {
		t.Errorf("one line without a trailing newline: %v", err)
	}
}

func encodeKey(keyring *Keyring) string {
	return base64.RawURLEncoding.EncodeToString(keyring.keys[0].key[:])
}

func writeKeyFile(t *testing.T, root *os.Root, content string) {
	t.Helper()
	if err := root.WriteFile(brand.SecretKeyFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCreate_NeverReplacesAKey(t *testing.T) {
	root := newRoot(t)
	first, second := Generate(), Generate()
	if err := Create(root, first); err != nil {
		t.Fatal(err)
	}
	if err := Create(root, second); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("got %v, want ErrKeyExists", err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.ID() != first.ID() {
		t.Fatalf("the first key must stay: %v, %v", loaded, err)
	}
	info, err := root.Stat(brand.SecretKeyFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v, %v", info, err)
	}
}

// Deux créations concurrentes finissent sur la même clé : chacune relit
// celle qui a gagné quand elle a perdu.
func TestCreate_ConcurrentCreatorsEndWithTheSameKey(t *testing.T) {
	root := newRoot(t)
	const creators = 8
	ids := make([]string, creators)
	var group sync.WaitGroup
	start := make(chan struct{})
	for i := range creators {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			keyring := Generate()
			err := Create(root, keyring)
			if errors.Is(err, ErrKeyExists) {
				keyring, err = Load(root)
			}
			if err != nil {
				t.Errorf("creator %d: %v", i, err)
				return
			}
			ids[i] = keyring.ID()
		}()
	}
	close(start)
	group.Wait()
	for i, id := range ids {
		if id != ids[0] {
			t.Fatalf("creator %d ended with key %s, creator 0 with %s", i, id, ids[0])
		}
	}
	loaded, err := Load(root)
	if err != nil || loaded.ID() != ids[0] {
		t.Fatalf("the file holds %v (%v), want %s", loaded, err, ids[0])
	}
}

// La rotation : une clé neuve scelle, les précédentes ouvrent encore ce
// qu'elles ont scellé, chacune reconnue par son identifiant ; le fichier
// garde l'ordre. Une fois tout rescellé, la neuve reste seule.
func TestRotation_SealsWithTheNewKeyAndOpensWithBoth(t *testing.T) {
	root := newRoot(t)
	old := Generate()
	sealedBefore := old.Seal("hunter2", Scope{Table: "settings", Column: "mail_password", Row: "1"})
	rotated := old.WithNewKey()
	if err := Save(root, rotated); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded.Len() != 2 || loaded.ID() != rotated.ID() || loaded.ID() == old.ID() {
		t.Fatalf("loaded %d keys, %v", loaded.Len(), err)
	}
	scope := Scope{Table: "settings", Column: "mail_password", Row: "1"}
	if plaintext, err := loaded.Open(sealedBefore, scope); err != nil || plaintext != "hunter2" {
		t.Fatalf("old secret: %q %v", plaintext, err)
	}
	sealedAfter := loaded.Seal("hunter2", scope)
	if !strings.HasPrefix(sealedAfter, "v1:"+rotated.ID()+":") {
		t.Fatalf("sealed with %q", sealedAfter)
	}
	current := loaded.Current()
	if _, err := current.Open(sealedBefore, scope); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("the current key alone opens an old secret: %v", err)
	}
	if loaded.Retired().Len() != 1 || loaded.Retired().ID() != old.ID() {
		t.Fatal("the retired key is not the old one")
	}
}
