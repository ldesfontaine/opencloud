package enroll

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTestRoot(t *testing.T) *os.Root {
	t.Helper()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("ouvrir le répertoire d'état : %v", err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func TestPrepareRemote_EngendreLaPaireDeLaMachineEtRendSaLignePublique(t *testing.T) {
	root := openTestRoot(t)

	publicKey, err := PrepareRemote(root, "temoin")
	if err != nil {
		t.Fatalf("PrepareRemote : %v", err)
	}

	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") || !strings.HasSuffix(publicKey, " opencloud@temoin") {
		t.Errorf("ligne publique : %q", publicKey)
	}
	if strings.Contains(publicKey, "\n") {
		t.Errorf("la ligne publique porte un saut de ligne : %q", publicKey)
	}

	written, err := root.ReadFile("machines/temoin/id_ed25519.pub")
	if err != nil {
		t.Fatalf("relire la clé publique : %v", err)
	}
	if strings.TrimSpace(string(written)) != publicKey {
		t.Errorf("le fichier dit %q", written)
	}

	info, err := os.Stat(filepath.Join(root.Name(), "machines/temoin/id_ed25519"))
	if err != nil {
		t.Fatalf("lire la clé privée : %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("clé privée en %04o, attendu 0600", info.Mode().Perm())
	}
}

func TestPrepareRemote_RejoueeNeRemplaceJamaisLaCle(t *testing.T) {
	root := openTestRoot(t)

	first, err := PrepareRemote(root, "temoin")
	if err != nil {
		t.Fatalf("PrepareRemote : %v", err)
	}
	privateKey, err := root.ReadFile("machines/temoin/id_ed25519")
	if err != nil {
		t.Fatalf("relire la clé privée : %v", err)
	}

	second, err := PrepareRemote(root, "temoin")
	if err != nil {
		t.Fatalf("PrepareRemote, second passage : %v", err)
	}

	if second != first {
		t.Errorf("la clé publique a changé : %q puis %q", first, second)
	}
	again, err := root.ReadFile("machines/temoin/id_ed25519")
	if err != nil {
		t.Fatalf("relire la clé privée : %v", err)
	}
	if string(again) != string(privateKey) {
		t.Error("la clé privée a été remplacée")
	}
}

func TestPrepareRemote_UnIdentifiantHorsFormeEstRefuse(t *testing.T) {
	root := openTestRoot(t)

	for _, id := range []string{"", "../autre", "Temoin", "a b"} {
		if _, err := PrepareRemote(root, id); err == nil {
			t.Errorf("%q accepté comme identifiant de machine", id)
		}
	}
}

func TestCommand_EstUneSeuleLigneAutonomeQuiPorteLeScriptEtLaCle(t *testing.T) {
	root := openTestRoot(t)
	publicKey, err := PrepareRemote(root, "temoin")
	if err != nil {
		t.Fatalf("PrepareRemote : %v", err)
	}

	command, err := Command(publicKey)
	if err != nil {
		t.Fatalf("Command : %v", err)
	}

	encoded, found := strings.CutPrefix(command, "echo '")
	if !found {
		t.Fatalf("commande : %q", command)
	}
	encoded, found = strings.CutSuffix(encoded, "' | base64 -d | sudo bash")
	if !found {
		t.Fatalf("commande : %q", command)
	}
	if strings.Contains(command, "\n") {
		t.Error("la commande doit tenir sur une seule ligne")
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("décoder la commande : %v", err)
	}
	if !strings.HasPrefix(string(decoded), "#!/bin/bash\n") {
		t.Errorf("le décodé ne commence pas par le shebang de lib.sh : %.30q", decoded)
	}
	if !strings.Contains(string(decoded), "\nexport OC_PUBLIC_KEY='"+publicKey+"'\n") {
		t.Error("le décodé ne pose pas OC_PUBLIC_KEY")
	}
	if !strings.Contains(string(decoded), "set -euo pipefail") {
		t.Error("le décodé ne porte pas l'en-tête commun")
	}
}

func TestCommand_UneCleQuiPorteraitUnGuillemetOuUnSautDeLigneEstRefusee(t *testing.T) {
	root := openTestRoot(t)
	publicKey, err := PrepareRemote(root, "temoin")
	if err != nil {
		t.Fatalf("PrepareRemote : %v", err)
	}

	for _, forged := range []string{
		publicKey + "'; reboot; '",
		publicKey + "\nrm -rf /",
		"",
		"ssh-rsa AAAAB3NzaC1yc2E tiers",
	} {
		if _, err := Command(forged); err == nil {
			t.Errorf("%q accepté comme clé publique", forged)
		}
	}
}
