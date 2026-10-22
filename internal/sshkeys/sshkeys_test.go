package sshkeys

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

const goodFingerprint = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"

func validKey() Key {
	return Key{File: FileAuthorizedKeys, Fingerprint: goodFingerprint, Type: "ssh-ed25519", Bits: 256, Comment: "lucas@pc", Options: []string{"command", "from"}}
}

func validAccount(name string) Account {
	return Account{Name: name, UID: 1000, State: StateRead, Keys: []Key{validKey()}}
}

func TestValidate_AReasonableReport_Passes(t *testing.T) {
	report := Report{Accounts: []Account{validAccount("lucas"), {Name: "root", UID: 0, State: StateDenied, Keys: []Key{}}}, Truncated: true}
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Report{}).Validate(); err != nil {
		t.Fatalf("un rapport vide : %v", err)
	}
}

func TestValidate_RefusesAReportOutOfBounds(t *testing.T) {
	tooManyAccounts := Report{}
	for index := range MaxAccounts + 1 {
		tooManyAccounts.Accounts = append(tooManyAccounts.Accounts, Account{Name: "u" + strconv.Itoa(index), State: StateNone})
	}
	tooManyKeys := Account{Name: "lucas", State: StateRead}
	for index := range MaxKeysPerAccount + 1 {
		key := validKey()
		key.Fingerprint = "SHA256:" + strings.Repeat("A", 42) + string(rune('A'+index%26))
		tooManyKeys.Keys = append(tooManyKeys.Keys, key)
	}
	overBudget := Report{}
	for index := range MaxKeysPerReport/MaxKeysPerAccount + 1 {
		account := Account{Name: "u" + strconv.Itoa(index), State: StateRead}
		for position := range MaxKeysPerAccount {
			key := validKey()
			key.Fingerprint = "SHA256:" + strings.Repeat("A", 40) + strconv.Itoa(10+position/26) + string(rune('A'+position%26))
			account.Keys = append(account.Keys, key)
		}
		overBudget.Accounts = append(overBudget.Accounts, account)
	}
	cases := map[string]Report{
		"65 comptes":                      tooManyAccounts,
		"65 clés pour un compte":          {Accounts: []Account{tooManyKeys}},
		"plus de clés que la borne":       overBudget,
		"deux fois le même compte":        {Accounts: []Account{validAccount("lucas"), validAccount("lucas")}},
		"état hors liste":                 {Accounts: []Account{{Name: "lucas", State: "hacked"}}},
		"état vide":                       {Accounts: []Account{{Name: "lucas"}}},
		"uid négatif":                     {Accounts: []Account{{Name: "lucas", UID: -1, State: StateNone}}},
		"uid trop grand":                  {Accounts: []Account{{Name: "lucas", UID: 4294967296, State: StateNone}}},
		"nom de compte avec un chemin":    {Accounts: []Account{{Name: "../root", State: StateNone}}},
		"nom de compte vide":              {Accounts: []Account{{Name: "", State: StateNone}}},
		"nom de compte trop long":         {Accounts: []Account{{Name: strings.Repeat("a", MaxAccountName+1), State: StateNone}}},
		"lignes ignorées négatives":       {Accounts: []Account{{Name: "lucas", State: StateRead, Ignored: -1}}},
		"lignes ignorées absurdes":        {Accounts: []Account{{Name: "lucas", State: StateRead, Ignored: MaxFileBytes + 1}}},
		"même clé deux fois":              {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{validKey(), validKey()}}}},
		"fichier hors liste":              {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.File = "../../etc/shadow" })}}}},
		"type hors liste":                 {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Type = "ssh-foo" })}}}},
		"empreinte mal formée":            {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Fingerprint = "SHA256:court" })}}}},
		"empreinte md5":                   {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Fingerprint = "MD5:" + strings.Repeat("a", 45) })}}}},
		"empreinte avec un caractère":     {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Fingerprint = "SHA256:" + strings.Repeat("A", 42) + "\n" })}}}},
		"bits négatifs":                   {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Bits = -1 })}}}},
		"bits absurdes":                   {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Bits = 1 << 30 })}}}},
		"commentaire avec échappement":    {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Comment = "\x1b[31mrouge" })}}}},
		"commentaire trop long":           {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Comment = strings.Repeat("a", MaxCommentBytes+1) })}}}},
		"commentaire UTF-8 invalide":      {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Comment = "a\xffb" })}}}},
		"option libre":                    {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Options = []string{`command="cat /etc/shadow"`} })}}}},
		"option hors liste":               {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Options = []string{"my-secret-option"} })}}}},
		"option en double":                {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Options = []string{"from", "from"} })}}}},
		"option en majuscules":            {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Options = []string{"FROM"} })}}}},
		"plus d'options que la liste":     {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Options = append(Options(), "from") })}}}},
		"séparateur d'option dans un nom": {Accounts: []Account{{Name: "lucas", State: StateRead, Keys: []Key{withKey(func(k *Key) { k.Options = []string{"from,command"} })}}}},
	}
	for name, report := range cases {
		t.Run(name, func(t *testing.T) {
			if err := report.Validate(); !errors.Is(err, ErrReportInvalid) {
				t.Fatalf("rapport accepté : %v", err)
			}
		})
	}
}

func withKey(change func(*Key)) Key {
	key := validKey()
	change(&key)
	return key
}

func TestValidate_TheBoundsOfTheAgentAreTheBoundsOfTheServer(t *testing.T) {
	account := Account{Name: strings.Repeat("a", MaxAccountName), State: StateRead}
	for index := range MaxKeysPerAccount {
		key := validKey()
		key.Fingerprint = "SHA256:" + strings.Repeat("A", 40) + strconv.Itoa(10+index/26) + string(rune('A'+index%26))
		key.Comment = strings.Repeat("é", MaxCommentBytes/2)
		key.Options = Options()
		account.Keys = append(account.Keys, key)
	}
	if err := (Report{Accounts: []Account{account}}).Validate(); err != nil {
		t.Fatalf("les bornes pleines sont refusées : %v", err)
	}
}

func TestCleanComment(t *testing.T) {
	cases := map[string]string{
		"lucas@pc":                         "lucas@pc",
		"  bords  ":                        "bords",
		"\x1b[31mrouge\x1b[0m":             "[31mrouge[0m",
		"a\u202eb":                         "ab",
		"a\x00b\x7fc\nd\te":                "abcde",
		"a\xffb":                           "ab",
		strings.Repeat("a", 100):           strings.Repeat("a", MaxCommentBytes),
		strings.Repeat("é", 100):           strings.Repeat("é", MaxCommentBytes/2),
		strings.Repeat("a", 63) + "é":      strings.Repeat("a", 63),
		strings.Repeat("a", 63) + " suite": strings.Repeat("a", 63),
		"":                                 "",
	}
	for input, want := range cases {
		got := CleanComment(input)
		if got != want {
			t.Errorf("CleanComment(%q) = %q, attendu %q", input, got, want)
		}
		if CleanComment(got) != got {
			t.Errorf("CleanComment n'est pas idempotent sur %q", input)
		}
	}
}

func TestIsAccountName(t *testing.T) {
	for _, name := range []string{"root", "lucas", "dev.ops@corp", "Jean-Pierre_2", strings.Repeat("a", MaxAccountName)} {
		if !IsAccountName(name) {
			t.Errorf("%q refusé", name)
		}
	}
	for _, name := range []string{"", "-rf", "a/b", "../x", "a b", "a\nb", "a:b", "é", strings.Repeat("a", MaxAccountName+1)} {
		if IsAccountName(name) {
			t.Errorf("%q accepté", name)
		}
	}
}

// deniedDir est un dossier que le compte de l'agent ne peut pas ouvrir, ou
// dont il ne peut rien lire : l'agent tourne en root dans ces tests, qui ne
// se voit refuser aucun fichier du disque.
type deniedDir struct {
	whereDenied map[string]bool
	// sshInfo est ce que Lstat dit de ~/.ssh quand il n'est pas refusé.
	sshInfo fs.FileInfo
}

func (d deniedDir) Lstat(name string) (fs.FileInfo, error) {
	if d.whereDenied[name] {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: syscall.EACCES}
	}
	return d.sshInfo, nil
}

func (d deniedDir) OpenFile(name string, _ int, _ fs.FileMode) (*os.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: syscall.EACCES}
}

func (deniedDir) Close() error { return nil }

func TestRead_PermissionRefused_IsDeniedAtEveryLevel(t *testing.T) {
	private := t.TempDir()
	if err := os.Chmod(private, 0o700); err != nil {
		t.Fatal(err)
	}
	sshInfo, err := os.Lstat(private)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		openHome func(string) (Dir, error)
	}{
		"le dossier personnel": {func(string) (Dir, error) {
			return nil, &fs.PathError{Op: "open", Path: "/home/lucas", Err: syscall.EACCES}
		}},
		"~/.ssh": {func(string) (Dir, error) { return deniedDir{map[string]bool{".ssh": true}, sshInfo}, nil }},
		"authorized_keys": {func(string) (Dir, error) {
			return deniedDir{map[string]bool{".ssh/authorized_keys": true, ".ssh/authorized_keys2": true}, sshInfo}, nil
		}},
		"une erreur inconnue": {func(string) (Dir, error) { return nil, errors.New("disque en panne") }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			reading := reader{openHome: c.openHome, ownerOf: ownedByTheAccount}

			report := reading.read(strings.NewReader("lucas:x:1000:1000::/home/lucas:/bin/bash\n"))

			if len(report.Accounts) != 1 || report.Accounts[0].State != StateDenied || len(report.Accounts[0].Keys) != 0 {
				t.Fatalf("rapport : %+v", report)
			}
		})
	}
}
