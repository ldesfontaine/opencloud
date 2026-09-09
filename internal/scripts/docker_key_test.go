package scripts

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// L'empreinte de la clé « Docker Release (CE deb) <docker@docker.com> » qui
// signe https://download.docker.com/linux/debian et /ubuntu — les deux dépôts
// que le socle ouvre (docs.docker.com/engine/install/debian/ et /ubuntu/).
// Une clé remplacée par erreur dans le dépôt fait tomber ce test.
const dockerKeyFingerprint = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"

func TestDockerKey_HasTheFingerprintDockerPublishes(t *testing.T) {
	key, err := DockerKey("socle")
	if err != nil {
		t.Fatalf("lire la clé de Docker : %v", err)
	}
	if key == "" {
		t.Fatal("le socle n'embarque aucune clé de dépôt")
	}

	fingerprint := fingerprintOfArmoredKey(t, key)
	if fingerprint != dockerKeyFingerprint {
		t.Fatalf("empreinte = %s, attendue %s : la clé embarquée n'est pas celle de Docker", fingerprint, dockerKeyFingerprint)
	}
}

func TestDockerKey_AnActionWithoutAKey_HasNone(t *testing.T) {
	key, err := DockerKey("diagnostiquer")
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if key != "" {
		t.Errorf("DockerKey = %.40q, attendu aucune clé", key)
	}
}

func TestCheckArmoredKey_RefusesWhatIsNotAnArmoredKey(t *testing.T) {
	body := "\nbWluaW1hbA==\n=abcd\n"
	for name, key := range map[string]string{
		"sans en-tête d'armure":  "bWluaW1hbA==\n",
		"sans fin d'armure":      keyArmorHeader + body,
		"un guillemet simple":    keyArmorHeader + "\nbWlu'aW1hbA==\n" + keyArmorFooter + "\n",
		"une substitution":       keyArmorHeader + "\n$(id)\n" + keyArmorFooter + "\n",
		"un retour chariot seul": keyArmorHeader + "\r\nbWluaW1hbA==\n" + keyArmorFooter + "\n",
	} {
		if err := checkArmoredKey("socle/docker.asc", key); err == nil {
			t.Errorf("%s devrait être refusé", name)
		}
	}

	valid := keyArmorHeader + body + keyArmorFooter + "\n"
	if err := checkArmoredKey("socle/docker.asc", valid); err != nil {
		t.Errorf("un bloc armuré bien formé est refusé : %v", err)
	}
}

// L'empreinte d'une clé OpenPGP v4 : SHA-1 sur le paquet de clé publique
// préfixé de 0x99 et de sa longueur (RFC 4880 §12.2). Bibliothèque standard
// seulement — pas de gpg à installer pour que ce test tourne, en CI comprise.
func fingerprintOfArmoredKey(t *testing.T, armored string) string {
	t.Helper()

	packets := decodeArmor(t, armored)
	if len(packets) < 3 {
		t.Fatalf("bloc armuré trop court : %d octets", len(packets))
	}
	// 0x99 : ancien format, étiquette 6 (clé publique), longueur sur deux
	// octets. C'est ce que Docker publie, et l'empreinte se calcule sur ces
	// trois octets suivis du corps du paquet.
	if packets[0] != 0x99 {
		t.Fatalf("premier paquet = %#x, attendu 0x99 (clé publique, longueur sur deux octets)", packets[0])
	}
	length := int(binary.BigEndian.Uint16(packets[1:3]))
	if len(packets) < 3+length {
		t.Fatalf("paquet de clé publique tronqué : %d octets annoncés, %d disponibles", length, len(packets)-3)
	}

	digest := sha1.Sum(packets[:3+length])
	return strings.ToUpper(hex.EncodeToString(digest[:]))
}

// decodeArmor rend les octets du bloc : les lignes base64 entre l'armure, sans
// la somme de contrôle finale qui commence par « = ».
func decodeArmor(t *testing.T, armored string) []byte {
	t.Helper()

	var encoded strings.Builder
	inside := false
	for _, line := range strings.Split(armored, "\n") {
		switch {
		case line == keyArmorHeader:
			inside = true
		case line == keyArmorFooter:
			inside = false
		case !inside || line == "" || strings.HasPrefix(line, "="):
			// En-têtes d'armure, ligne vide, somme de contrôle : rien à décoder.
		default:
			encoded.WriteString(line)
		}
	}

	packets, err := base64.StdEncoding.DecodeString(encoded.String())
	if err != nil {
		t.Fatalf("décoder l'armure : %v", err)
	}
	return packets
}
