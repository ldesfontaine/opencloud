package restic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// sampleHash a la forme d'une empreinte bcrypt sans en payer le calcul.
func sampleHash(seed string) string {
	return "$2y$10$" + strings.Repeat(seed, 53)
}

func TestHashPassword_WritesTheHtpasswdFormThatStillVerifies(t *testing.T) {
	password := []byte("rest-password-never-leaves-the-source")

	hash, err := HashPassword(password)

	if err != nil || !strings.HasPrefix(hash, "$2y$10$") {
		t.Fatalf("hash %q %v", hash, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), password) != nil {
		t.Error("the $2y$ form does not verify")
	}
	if strings.Contains(hash, string(password)) {
		t.Error("hash carries the password")
	}
}

func TestHtpasswd_SetRemoveAndWriteAtomically(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	hashA, hashB := sampleHash("a"), sampleHash("b")

	entries, err := ReadHtpasswd(root, "htpasswd")
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing file: %v %v", entries, err)
	}
	entries, _ = SetHtpasswdUser(entries, HtpasswdEntry{User: "vps", Hash: hashB})
	entries, _ = SetHtpasswdUser(entries, HtpasswdEntry{User: "nas", Hash: hashA})
	entries, _ = SetHtpasswdUser(entries, HtpasswdEntry{User: "vps", Hash: hashA})
	if err := WriteHtpasswd(root, "htpasswd", entries); err != nil {
		t.Fatal(err)
	}

	content, _ := root.ReadFile("htpasswd")
	if string(content) != "nas:"+hashA+"\nvps:"+hashA+"\n" {
		t.Fatalf("content %q", content)
	}
	info, _ := os.Stat(filepath.Join(root.Name(), "htpasswd"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode())
	}
	reread, err := ReadHtpasswd(root, "htpasswd")
	if err != nil || len(reread) != 2 {
		t.Fatalf("reread %v %v", reread, err)
	}
	if left := RemoveHtpasswdUser(reread, "vps"); len(left) != 1 || left[0].User != "nas" || len(reread) != 2 {
		t.Errorf("remove %v from %v", left, reread)
	}
}

// Un nom qui sortirait du dépôt, ou une ligne qui en ajouterait une autre,
// n'entre pas dans le fichier.
func TestHtpasswd_RefusesHostileUsersAndHashes(t *testing.T) {
	hash := sampleHash("a")
	hostile := []HtpasswdEntry{
		{User: "", Hash: hash},
		{User: "../etc", Hash: hash},
		{User: "nas:x", Hash: hash},
		{User: "Nas", Hash: hash},
		{User: "nas\nroot", Hash: hash},
		{User: "-nas", Hash: hash},
		{User: strings.Repeat("a", 64), Hash: hash},
		{User: "nas", Hash: "plain"},
		{User: "nas", Hash: hash + "\nroot:" + hash},
		{User: "nas", Hash: "{SHA}abc"},
	}
	for _, entry := range hostile {
		if _, err := SetHtpasswdUser(nil, entry); err == nil {
			t.Errorf("%q passed", entry.User+":"+entry.Hash)
		}
	}
	if _, err := ParseHtpasswd([]byte("nas:" + hash + "\nbroken\n")); err == nil {
		t.Error("a broken line passed")
	}
}
