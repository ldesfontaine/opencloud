package sshkeys

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Les tests de lecture sont hostiles : l'utilisateur d'un compte contrôle son
// dossier personnel, et l'agent le lit sans lui faire confiance.

const accountUID = 1000

// ownedByTheAccount : tout fichier appartient au compte. Les tests tournent en
// root ici et sous un autre uid en CI : le propriétaire réel ne se choisit pas.
func ownedByTheAccount(fs.FileInfo) (uint32, bool) { return accountUID, true }

func testReader(owner func(fs.FileInfo) (uint32, bool)) reader {
	return reader{openHome: OpenHome, ownerOf: owner}
}

// newHome crée un dossier personnel avec son ~/.ssh en 0700.
func newHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// Le umask a pu retirer des droits : le mode voulu est posé tel quel.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func authorized(home, name string) string {
	return filepath.Join(home, ".ssh", name)
}

// publicKeyLine rend la ligne d'une clé ed25519 neuve, avec ses options et
// son commentaire, et son empreinte.
func publicKeyLine(t *testing.T, options, comment string) (line, fingerprint string) {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return lineOf(t, public, options, comment)
}

func lineOf(t *testing.T, public any, options, comment string) (line, fingerprint string) {
	t.Helper()
	key, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	line = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	if options != "" {
		line = options + " " + line
	}
	if comment != "" {
		line += " " + comment
	}
	return line, ssh.FingerprintSHA256(key)
}

func passwdOf(home string) string {
	return "root:x:0:0:root:/nonexistent-root:/bin/bash\nlucas:x:" + strconv.Itoa(accountUID) + ":1000::" + home + ":/bin/bash\n"
}

// readLucas lit le seul compte lucas d'un passwd qui nomme aussi root, dont le
// dossier n'existe pas.
func readLucas(t *testing.T, r reader, home string) Account {
	t.Helper()
	report := r.read(strings.NewReader(passwdOf(home)))
	for _, account := range report.Accounts {
		if account.Name == "lucas" {
			return account
		}
	}
	t.Fatalf("lucas absent du rapport : %+v", report)
	return Account{}
}

func TestRead_ValidKeys_ReportsFingerprintTypeBitsAndCommentOnly(t *testing.T) {
	home := newHome(t)
	edLine, edFingerprint := publicKeyLine(t, "", "lucas@pc")
	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	rsaLine, rsaFingerprint := lineOf(t, &rsaKey.PublicKey, "", "vieux-portable")
	ecKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecLine, ecFingerprint := lineOf(t, &ecKey.PublicKey, "", "")
	writeFile(t, authorized(home, "authorized_keys"), "# mes clés\n\n"+edLine+"\n"+rsaLine+"\r\n"+ecLine+"\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateRead || account.Ignored != 0 || account.Truncated || len(account.Keys) != 3 {
		t.Fatalf("compte : %+v", account)
	}
	want := []Key{
		{File: "authorized_keys", Fingerprint: edFingerprint, Type: "ssh-ed25519", Bits: 256, Comment: "lucas@pc", Options: []string{}},
		{File: "authorized_keys", Fingerprint: rsaFingerprint, Type: "ssh-rsa", Bits: 1024, Comment: "vieux-portable", Options: []string{}},
		{File: "authorized_keys", Fingerprint: ecFingerprint, Type: "ecdsa-sha2-nistp384", Bits: 384, Comment: "", Options: []string{}},
	}
	for index, key := range want {
		got := account.Keys[index]
		if got.File != key.File || got.Fingerprint != key.Fingerprint || got.Type != key.Type || got.Bits != key.Bits || got.Comment != key.Comment || len(got.Options) != 0 {
			t.Errorf("clé %d = %+v, attendu %+v", index, got, key)
		}
	}
}

func TestRead_SecurityKey_ReportsItsType(t *testing.T) {
	home := newHome(t)
	blob := securityKeyBlob("sk-ssh-ed25519@openssh.com", make([]byte, 32), "ssh:")
	public, err := ssh.ParsePublicKey(blob)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, authorized(home, "authorized_keys"), "sk-ssh-ed25519@openssh.com "+base64.StdEncoding.EncodeToString(blob)+" yubikey\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if len(account.Keys) != 1 || account.Keys[0].Type != "sk-ssh-ed25519@openssh.com" || account.Keys[0].Bits != 256 || account.Keys[0].Fingerprint != ssh.FingerprintSHA256(public) {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_ForcedCommandAndFromOption_OnlyTheirNamesLeave(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, `command="cat /etc/shadow",from="10.0.0.1",no-pty,Restrict,permitopen="db:5432",mon-option="secret-9f"`, "lucas@pc")
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if len(account.Keys) != 1 {
		t.Fatalf("compte : %+v", account)
	}
	if got := strings.Join(account.Keys[0].Options, ","); got != "command,from,no-pty,restrict,permitopen,other" {
		t.Fatalf("options = %q", got)
	}
	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"shadow", "10.0.0.1", "db:5432", "secret-9f", "mon-option"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("le rapport laisse passer %q : %s", leaked, encoded)
		}
	}
}

func TestRead_NeverReportsAFileContent(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	writeFile(t, authorized(home, "authorized_keys"),
		"# TOKEN-EN-COMMENTAIRE-DE-FICHIER\nPASSWORD=hunter2\n"+line+"\nceci n'est pas une clé SECRET-LIGNE-LIBRE\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.Ignored != 2 || len(account.Keys) != 1 {
		t.Fatalf("compte : %+v", account)
	}
	encoded, _ := json.Marshal(account)
	for _, leaked := range []string{"TOKEN-EN-COMMENTAIRE", "hunter2", "SECRET-LIGNE-LIBRE"} {
		if strings.Contains(string(encoded), leaked) {
			t.Errorf("le rapport laisse passer %q : %s", leaked, encoded)
		}
	}
}

func TestRead_SSHDirectoryIsASymlink_NothingIsRead(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	line, fingerprint := publicKeyLine(t, "", "etranger")
	writeFile(t, filepath.Join(elsewhere, "authorized_keys"), line+"\n", 0o600)
	if err := os.Symlink(elsewhere, filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateUnsafe || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
	if encoded, _ := json.Marshal(account); strings.Contains(string(encoded), fingerprint) {
		t.Fatal("une clé hors du dossier personnel est sortie")
	}
}

func TestRead_SSHDirectoryLinksToEtc_NothingIsRead(t *testing.T) {
	home := t.TempDir()
	if err := os.Symlink("/etc", filepath.Join(home, ".ssh")); err != nil {
		t.Fatal(err)
	}

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateUnsafe || len(account.Keys) != 0 || account.Ignored != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_AuthorizedKeysIsASymlink_NothingIsRead(t *testing.T) {
	outside := t.TempDir()
	line, fingerprint := publicKeyLine(t, "", "dehors")
	writeFile(t, filepath.Join(outside, "target"), line+"\n", 0o600)
	inside := newHome(t)
	writeFile(t, filepath.Join(inside, "real"), line+"\n", 0o600)
	cases := map[string]string{"hors du dossier": filepath.Join(outside, "target"), "dans le dossier": filepath.Join(inside, "real")}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			home := newHome(t)
			if err := os.Symlink(target, authorized(home, "authorized_keys")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/etc/shadow", authorized(home, "authorized_keys2")); err != nil {
				t.Fatal(err)
			}

			account := readLucas(t, testReader(ownedByTheAccount), home)

			if account.State != StateUnsafe || len(account.Keys) != 0 || account.Ignored != 0 {
				t.Fatalf("compte : %+v", account)
			}
			if encoded, _ := json.Marshal(account); strings.Contains(string(encoded), fingerprint) {
				t.Fatal("la clé d'une cible de lien est sortie")
			}
		})
	}
}

// Un lien dur n'est pas un lien symbolique : O_NOFOLLOW ne l'arrête pas, et
// un second nom de /etc/shadow appartient à root, sans droit d'écriture pour
// les autres. Sans protected_hardlinks, un compte peut en poser un dans son
// ~/.ssh : l'agent root ne doit rien en lire, pas même le nombre de lignes.
func TestRead_AuthorizedKeysIsAHardLinkToAFakeShadow_NothingLeaves(t *testing.T) {
	etc := t.TempDir()
	shadow := filepath.Join(etc, "shadow")
	// Un faux shadow, et même une vraie clé dedans : rien n'en sort.
	line, fingerprint := publicKeyLine(t, "", "commentaire-du-shadow")
	writeFile(t, shadow, "root:$6$faux-sel$faux-condensat-du-shadow:19000:0:99999:7:::\n"+line+"\n", 0o640)
	home := newHome(t)
	for _, file := range Files() {
		if err := os.Link(shadow, authorized(home, file)); err != nil {
			t.Fatal(err)
		}
	}
	ownedByRoot := func(fs.FileInfo) (uint32, bool) { return 0, true }

	report := testReader(ownedByRoot).read(strings.NewReader(passwdOf(home)))

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"shadow", "faux", "19000", fingerprint} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("%q est sorti du faux shadow : %s", leaked, encoded)
		}
	}
	account := readLucas(t, testReader(ownedByRoot), home)
	if account.State != StateUnsafe || len(account.Keys) != 0 || account.Ignored != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_FileWritableByOthers_IsUnsafe(t *testing.T) {
	for _, mode := range []os.FileMode{0o666, 0o662, 0o664, 0o620} {
		t.Run(mode.String(), func(t *testing.T) {
			home := newHome(t)
			line, _ := publicKeyLine(t, "", "")
			writeFile(t, authorized(home, "authorized_keys"), line+"\n", mode)

			account := readLucas(t, testReader(ownedByTheAccount), home)

			if account.State != StateUnsafe || len(account.Keys) != 0 {
				t.Fatalf("compte : %+v", account)
			}
		})
	}
}

func TestRead_SSHDirectoryWritableByOthers_IsUnsafe(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)
	if err := os.Chmod(filepath.Join(home, ".ssh"), 0o777); err != nil {
		t.Fatal(err)
	}

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateUnsafe || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_FileOwnedBySomeoneElse_IsUnsafe_RootIsAccepted(t *testing.T) {
	line, _ := publicKeyLine(t, "", "")
	owners := map[string]struct {
		uid  uint32
		want State
	}{"un autre compte": {4242, StateUnsafe}, "root": {0, StateRead}, "le compte": {accountUID, StateRead}}
	for name, owner := range owners {
		t.Run(name, func(t *testing.T) {
			home := newHome(t)
			writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)
			ownedBy := func(fs.FileInfo) (uint32, bool) { return owner.uid, true }

			account := readLucas(t, testReader(ownedBy), home)

			if account.State != owner.want {
				t.Fatalf("état %q, attendu %q", account.State, owner.want)
			}
			if owner.want == StateUnsafe && len(account.Keys) != 0 {
				t.Fatalf("clés lues d'un fichier douteux : %+v", account.Keys)
			}
		})
	}
}

func TestRead_OwnerUnknown_IsUnsafe(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)

	account := readLucas(t, testReader(func(fs.FileInfo) (uint32, bool) { return 0, false }), home)

	if account.State != StateUnsafe {
		t.Fatalf("compte : %+v", account)
	}
}

func TestFileOwner_ReadsTheRealOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	writeFile(t, path, "x", 0o600)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, known := fileOwner(info)
	if !known || int(owner) != os.Getuid() {
		t.Fatalf("propriétaire %d (connu : %v), attendu %d", owner, known, os.Getuid())
	}
}

func TestRead_NamedPipe_IsUnsafeAndNeverBlocks(t *testing.T) {
	home := newHome(t)
	if err := syscall.Mkfifo(authorized(home, "authorized_keys"), 0o600); err != nil {
		t.Skipf("pas de tube nommé ici : %v", err)
	}
	if err := syscall.Mkfifo(authorized(home, "authorized_keys2"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan Account, 1)
	go func() { done <- readLucas(t, testReader(ownedByTheAccount), home) }()

	select {
	case account := <-done:
		if account.State != StateUnsafe || len(account.Keys) != 0 {
			t.Fatalf("compte : %+v", account)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("la lecture d'un tube nommé bloque")
	}
}

// lyingDir fait mentir Lstat sur un nom : le fichier a été échangé entre le
// jugement et l'ouverture.
type lyingDir struct {
	Dir
	lie  string
	info fs.FileInfo
	open func(name string) (*os.File, error)
}

func (d lyingDir) Lstat(name string) (fs.FileInfo, error) {
	if name == d.lie {
		return d.info, nil
	}
	return d.Dir.Lstat(name)
}

func (d lyingDir) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	if name == d.lie && d.open != nil {
		return d.open(name)
	}
	return d.Dir.OpenFile(name, flag, perm)
}

func TestRead_FileSwappedForADeviceAfterTheJudgement_IsUnsafeAndNotRead(t *testing.T) {
	home := newHome(t)
	decoy := filepath.Join(t.TempDir(), "decoy")
	writeFile(t, decoy, "rien\n", 0o600)
	info, err := os.Lstat(decoy)
	if err != nil {
		t.Fatal(err)
	}
	reading := testReader(ownedByTheAccount)
	reading.openHome = func(path string) (Dir, error) {
		dir, err := OpenHome(path)
		if err != nil {
			return nil, err
		}
		return lyingDir{Dir: dir, lie: ".ssh/authorized_keys", info: info, open: func(string) (*os.File, error) { return os.Open("/dev/zero") }}, nil
	}

	account := readLucas(t, reading, home)

	if account.State != StateUnsafe || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_FileSwappedForASymlinkAfterTheJudgement_IsNotFollowed(t *testing.T) {
	home := newHome(t)
	line, fingerprint := publicKeyLine(t, "", "")
	writeFile(t, filepath.Join(home, "secret"), line+"\n", 0o600)
	if err := os.Symlink(filepath.Join(home, "secret"), authorized(home, "authorized_keys")); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(t.TempDir(), "decoy")
	writeFile(t, decoy, "rien\n", 0o600)
	info, err := os.Lstat(decoy)
	if err != nil {
		t.Fatal(err)
	}
	reading := testReader(ownedByTheAccount)
	reading.openHome = func(path string) (Dir, error) {
		dir, err := OpenHome(path)
		if err != nil {
			return nil, err
		}
		return lyingDir{Dir: dir, lie: ".ssh/authorized_keys", info: info}, nil
	}

	account := readLucas(t, reading, home)

	if account.State != StateUnsafe || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
	if encoded, _ := json.Marshal(account); strings.Contains(string(encoded), fingerprint) {
		t.Fatal("le lien a été suivi")
	}
}

func TestRead_HugeFile_IsTooLargeAndNotParsed(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	padding := strings.Repeat("# bourrage\n", (MaxFileBytes/11)+10)
	writeFile(t, authorized(home, "authorized_keys"), line+"\n"+padding, 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateTooLarge || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_FileJustUnderTheLimit_IsRead(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	content := line + "\n"
	content += strings.Repeat("#", MaxFileBytes-len(content)-1) + "\n"
	writeFile(t, authorized(home, "authorized_keys"), content, 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateRead || len(account.Keys) != 1 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_HugeLine_IsIgnoredAndTheNextKeyIsRead(t *testing.T) {
	home := newHome(t)
	line, fingerprint := publicKeyLine(t, "", "")
	huge := "ssh-ed25519 " + strings.Repeat("A", 150<<10)
	hugeOption := `command="` + strings.Repeat("x", 60<<10) + `" ` + line
	writeFile(t, authorized(home, "authorized_keys"), huge+"\n"+hugeOption+"\n"+line+"\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateRead || account.Ignored != 2 || len(account.Keys) != 1 || account.Keys[0].Fingerprint != fingerprint {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_Comment_IsCleanedAndBounded(t *testing.T) {
	home := newHome(t)
	hostile := "\x1b[31mrouge\x1b[0m\u202egnp.exe " + strings.Repeat("é", 200)
	line, _ := publicKeyLine(t, "", hostile)
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if len(account.Keys) != 1 {
		t.Fatalf("compte : %+v", account)
	}
	comment := account.Keys[0].Comment
	if len(comment) > MaxCommentBytes || !strings.HasPrefix(comment, "[31mrouge[0mgnp.exe") {
		t.Fatalf("commentaire %q (%d octets)", comment, len(comment))
	}
	for _, character := range comment {
		if character < ' ' || character == 0x7f || character == '\u202e' || character == '�' {
			t.Fatalf("caractère %U dans %q", character, comment)
		}
	}
}

func TestRead_InvalidKeysAndForeignTypes_AreCountedAsIgnored(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	certificate := "ssh-ed25519-cert-v01@openssh.com AAAAIHNzaC1lZDI1NTE5LWNlcnQtdjAxQG9wZW5zc2guY29tAAAA bad"
	writeFile(t, authorized(home, "authorized_keys"), strings.Join([]string{
		"ssh-foo AAAAB3NzaC1mb28= inconnu",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI-pas-du-base64",
		"ssh-ed25519",
		certificate,
		"\x00\x01\x02",
		line,
		"",
	}, "\n"), 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateRead || account.Ignored != 5 || len(account.Keys) != 1 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_SameKeyTwice_IsOneKeyWithBothOptionSets(t *testing.T) {
	home := newHome(t)
	plain, fingerprint := publicKeyLine(t, "", "")
	restricted := `from="10.0.0.1",no-pty ` + strings.TrimPrefix(plain, "")
	writeFile(t, authorized(home, "authorized_keys"), restricted+"\n"+plain+"\n", 0o600)
	writeFile(t, authorized(home, "authorized_keys2"), plain+"\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if len(account.Keys) != 2 || account.Keys[0].Fingerprint != fingerprint || account.Keys[0].File != "authorized_keys" || account.Keys[1].File != "authorized_keys2" {
		t.Fatalf("compte : %+v", account)
	}
	if got := strings.Join(account.Keys[0].Options, ","); got != "from,no-pty" {
		t.Fatalf("options = %q", got)
	}
	if err := (Report{Accounts: []Account{account}}).Validate(); err != nil {
		t.Fatalf("un rapport sans doublon refusé : %v", err)
	}
}

func TestRead_SixtyFiveKeys_KeepsSixtyFourAndSaysTruncated(t *testing.T) {
	home := newHome(t)
	var lines []string
	for range MaxKeysPerAccount + 1 {
		line, _ := publicKeyLine(t, "", "")
		lines = append(lines, line)
	}
	writeFile(t, authorized(home, "authorized_keys"), strings.Join(lines, "\n")+"\n", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if len(account.Keys) != MaxKeysPerAccount || !account.Truncated {
		t.Fatalf("%d clés, tronqué : %v", len(account.Keys), account.Truncated)
	}
}

func TestRead_ManyAccountsOfManyKeys_StopsAtTheReportBudget(t *testing.T) {
	var lines []string
	for range MaxKeysPerAccount {
		line, _ := publicKeyLine(t, "", "")
		lines = append(lines, line)
	}
	content := strings.Join(lines, "\n") + "\n"
	var passwd strings.Builder
	for index := range MaxKeysPerReport/MaxKeysPerAccount + 2 {
		home := newHome(t)
		writeFile(t, authorized(home, "authorized_keys"), content, 0o600)
		passwd.WriteString("u" + strconv.Itoa(index) + ":x:" + strconv.Itoa(2000+index) + ":1000::" + home + ":/bin/sh\n")
	}

	ownedByRoot := func(fs.FileInfo) (uint32, bool) { return 0, true }
	report := testReader(ownedByRoot).read(strings.NewReader(passwd.String()))

	if countSSHKeys(report) != MaxKeysPerReport {
		t.Fatalf("%d clés rapportées, borne %d", countSSHKeys(report), MaxKeysPerReport)
	}
	if err := report.Validate(); err != nil {
		t.Fatalf("un rapport borné par l'agent doit passer le serveur : %v", err)
	}
	last := report.Accounts[len(report.Accounts)-1]
	if !last.Truncated || len(last.Keys) != 0 {
		t.Fatalf("dernier compte : %+v", last)
	}
}

func countSSHKeys(report Report) int {
	total := 0
	for _, account := range report.Accounts {
		total += len(account.Keys)
	}
	return total
}

func TestRead_SeventyAccounts_KeepsSixtyFour(t *testing.T) {
	var passwd strings.Builder
	passwd.WriteString("root:x:0:0:root:/nonexistent-root:/bin/sh\n")
	for index := range 70 {
		passwd.WriteString("u" + strconv.Itoa(index) + ":x:" + strconv.Itoa(2000+index) + ":1000::/nonexistent-home:/bin/sh\n")
	}

	report := testReader(ownedByTheAccount).read(strings.NewReader(passwd.String()))

	if len(report.Accounts) != MaxAccounts || !report.Truncated {
		t.Fatalf("%d comptes, tronqué : %v", len(report.Accounts), report.Truncated)
	}
	if report.Accounts[0].Name != "root" {
		t.Fatalf("premier compte %q", report.Accounts[0].Name)
	}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRead_ExactlySixtyFourAccounts_IsNotTruncated(t *testing.T) {
	var passwd strings.Builder
	for index := range MaxAccounts {
		passwd.WriteString("u" + strconv.Itoa(index) + ":x:" + strconv.Itoa(2000+index) + ":1000::/nonexistent-home:/bin/sh\n")
	}

	report := testReader(ownedByTheAccount).read(strings.NewReader(passwd.String()))

	if len(report.Accounts) != MaxAccounts || report.Truncated {
		t.Fatalf("%d comptes, tronqué : %v", len(report.Accounts), report.Truncated)
	}
}

func TestRead_PasswdLines_OnlyRootAndRealUsersWithACleanHomeAreRead(t *testing.T) {
	passwd := strings.Join([]string{
		"root:x:0:0:root:/root:/bin/bash",
		"daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin",
		"sys:x:999:999::/home/sys:/bin/sh",
		"nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin",
		"neg:x:4294967295:1::/home/neg:/bin/sh",
		"toobig:x:4294967296:1::/home/toobig:/bin/sh",
		"relative:x:1001:1001::home/relative:/bin/sh",
		"dotdot:x:1002:1002::/home/../etc:/bin/sh",
		"unclean:x:1003:1003::/home//unclean:/bin/sh",
		"slash:x:1004:1004::/:/bin/sh",
		"trailing:x:1005:1005::/home/trailing/:/bin/sh",
		"bad/name:x:1006:1006::/home/badname:/bin/sh",
		"-dash:x:1007:1007::/home/dash:/bin/sh",
		"short:x:1008",
		"lucas:x:1000:1000:Lucas:/home/lucas:/bin/bash",
		"lucas:x:1009:1009:Doublon:/home/doublon:/bin/bash",
		"dev.ops@corp:x:1010:1010::/home/devops:/bin/bash",
		"# commentaire",
		"",
		"long:x:1011:1011:" + strings.Repeat("g", 2000) + ":/home/long:/bin/sh",
	}, "\n")

	entries, truncated := listAccounts(strings.NewReader(passwd))

	var names []string
	for _, entry := range entries {
		names = append(names, entry.name)
	}
	if truncated || strings.Join(names, ",") != "root,lucas,dev.ops@corp" {
		t.Fatalf("comptes lus : %v (tronqué : %v)", names, truncated)
	}
	if entries[1].home != "/home/lucas" || entries[1].uid != 1000 {
		t.Fatalf("lucas : %+v", entries[1])
	}
}

func TestRead_HomeThatDoesNotExist_IsNone(t *testing.T) {
	account := readLucas(t, testReader(ownedByTheAccount), filepath.Join(t.TempDir(), "absent"))

	if account.State != StateNone || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_HomeThatIsAFile_IsNone(t *testing.T) {
	home := filepath.Join(t.TempDir(), "fichier")
	writeFile(t, home, "x", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateNone {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_AccountWithoutSSHDirectory_IsNone(t *testing.T) {
	account := readLucas(t, testReader(ownedByTheAccount), t.TempDir())

	if account.State != StateNone || account.Keys == nil {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_EmptyFile_IsReadWithoutKeys(t *testing.T) {
	home := newHome(t)
	writeFile(t, authorized(home, "authorized_keys"), "", 0o600)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateRead || len(account.Keys) != 0 {
		t.Fatalf("compte : %+v", account)
	}
}

func TestRead_HomeUnreadableForTheAgent_IsDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root lit tout : le refus d'un autre compte ne se voit pas en root")
	}
	home := newHome(t)
	line, _ := publicKeyLine(t, "", "")
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)
	cases := map[string]func(){
		"dossier personnel fermé": func() { _ = os.Chmod(home, 0o000) },
		"~/.ssh fermé":            func() { _ = os.Chmod(filepath.Join(home, ".ssh"), 0o000) },
		"fichier fermé":           func() { _ = os.Chmod(authorized(home, "authorized_keys"), 0o000) },
	}
	for name, close := range cases {
		t.Run(name, func(t *testing.T) {
			close()
			t.Cleanup(func() {
				_ = os.Chmod(home, 0o700)
				_ = os.Chmod(filepath.Join(home, ".ssh"), 0o700)
				_ = os.Chmod(authorized(home, "authorized_keys"), 0o600)
			})

			account := readLucas(t, testReader(ownedByTheAccount), home)

			if account.State != StateDenied || len(account.Keys) != 0 {
				t.Fatalf("compte : %+v", account)
			}
		})
	}
}

func TestRead_OneFileDeniedOrUnsafe_TheAccountSaysSoButKeepsTheOtherKeys(t *testing.T) {
	home := newHome(t)
	line, fingerprint := publicKeyLine(t, "", "")
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)
	writeFile(t, authorized(home, "authorized_keys2"), line+"\n", 0o666)

	account := readLucas(t, testReader(ownedByTheAccount), home)

	if account.State != StateUnsafe || len(account.Keys) != 1 || account.Keys[0].Fingerprint != fingerprint {
		t.Fatalf("compte : %+v", account)
	}
}

func TestCombineStates_TheWorstWins(t *testing.T) {
	cases := []struct {
		states []State
		want   State
	}{
		{[]State{StateNone, StateNone}, StateNone},
		{[]State{StateRead, StateNone}, StateRead},
		{[]State{StateNone, StateRead}, StateRead},
		{[]State{StateRead, StateTooLarge}, StateTooLarge},
		{[]State{StateTooLarge, StateUnsafe}, StateUnsafe},
		{[]State{StateRead, StateDenied}, StateDenied},
		{[]State{StateUnsafe, StateDenied}, StateDenied},
	}
	for _, c := range cases {
		if got := combineStates(c.states); got != c.want {
			t.Errorf("%v = %q, attendu %q", c.states, got, c.want)
		}
	}
}

func TestRead_ARealReportPassesTheServersValidation(t *testing.T) {
	home := newHome(t)
	line, _ := publicKeyLine(t, `command="x",from="y"`, "lucas@pc")
	writeFile(t, authorized(home, "authorized_keys"), line+"\n", 0o600)

	report := testReader(ownedByTheAccount).read(strings.NewReader(passwdOf(home)))

	if err := report.Validate(); err != nil {
		t.Fatalf("le rapport de l'agent est refusé : %v", err)
	}
}

func TestOpenHome_AFileIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	writeFile(t, path, "x", 0o600)
	if _, err := OpenHome(path); err == nil {
		t.Fatal("un fichier s'ouvre comme dossier personnel")
	}
}

func TestListAccounts_PasswdTooBig_IsCut(t *testing.T) {
	reader := io.LimitReader(zeroLines{}, maxPasswdBytes*4)
	entries, _ := listAccounts(reader)
	if len(entries) != 0 {
		t.Fatalf("%d comptes lus de lignes vides", len(entries))
	}
}

// zeroLines est un flux de sauts de ligne sans fin.
type zeroLines struct{}

func (zeroLines) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = '\n'
	}
	return len(buffer), nil
}

// securityKeyBlob écrit la clé publique d'une clé de sécurité (format du fil
// SSH) : le type, la clé, l'application.
func securityKeyBlob(keyType string, key []byte, application string) []byte {
	var blob []byte
	for _, field := range [][]byte{[]byte(keyType), key, []byte(application)} {
		blob = binary.BigEndian.AppendUint32(blob, uint32(len(field)))
		blob = append(blob, field...)
	}
	return blob
}
