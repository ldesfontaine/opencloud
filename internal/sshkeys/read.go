package sshkeys

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
)

// La lecture, côté agent. Le dossier personnel d'un compte est le terrain de
// son utilisateur : il y pose des liens, des fichiers énormes, des tubes. On
// le lit donc comme un inconnu : ouvert par os.Root (rien ne sort du dossier),
// chaque fichier jugé avant d'être ouvert, puis rejugé une fois ouvert, et
// ce qu'il contient ne sort jamais tel quel.

const (
	// Un passwd plus gros est coupé ; une ligne plus longue est ignorée.
	maxPasswdBytes = 4 << 20
	maxPasswdLine  = 1024
	// Les uid réservés : nobody, et -1 (« pas d'uid »).
	nobodyUID  = 65534
	noUID      = 4294967295
	firstUser  = 1000
	sshDirName = ".ssh"
)

// Dir est le dossier personnel d'un compte, ouvert et confiné : *os.Root en
// production, un faux dans les tests.
type Dir interface {
	Lstat(name string) (fs.FileInfo, error)
	OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error)
	Close() error
}

// OpenHome ouvre un dossier personnel pour le lire sans jamais en sortir.
func OpenHome(home string) (Dir, error) {
	root, err := os.OpenRoot(home) // #nosec G304 G703 -- home vient de /etc/passwd (la machine), pas d'un ordre ; os.Root confine tout ce qui est lu dessous.
	if err != nil {
		return nil, explainOpenError(home, err)
	}
	return root, nil
}

// explainOpenError : os.Root dit « not a directory » par une erreur qui n'est
// pas ENOTDIR ; un « dossier » qui est un fichier est un dossier absent.
func explainOpenError(home string, err error) error {
	if info, statErr := os.Stat(home); statErr == nil && !info.IsDir() { // #nosec G703 -- home vient de /etc/passwd ; un Stat ne lit rien.
		return fmt.Errorf("home is not a directory: %w", fs.ErrNotExist)
	}
	return err
}

// ReadAll relève les clés autorisées des comptes de passwd : root et les uid
// d'utilisateur, avec un dossier personnel. openHome ouvre un dossier.
func ReadAll(passwd io.Reader, openHome func(home string) (Dir, error)) Report {
	return reader{openHome: openHome, ownerOf: fileOwner}.read(passwd)
}

// reader porte les lectures que les tests remplacent : les tests tournent en
// root ici et en simple utilisateur en CI, le propriétaire d'un fichier ne
// s'y choisit pas.
type reader struct {
	openHome func(home string) (Dir, error)
	ownerOf  func(info fs.FileInfo) (uid uint32, known bool)
}

func fileOwner(info fs.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}

func (r reader) read(passwd io.Reader) Report {
	entries, truncated := listAccounts(passwd)
	report := Report{Accounts: make([]Account, 0, len(entries)), Truncated: truncated}
	room := MaxKeysPerReport
	for _, entry := range entries {
		account := r.readAccount(entry, room)
		room -= len(account.Keys)
		report.Accounts = append(report.Accounts, account)
	}
	return report
}

type passwdEntry struct {
	name string
	uid  uint32
	home string
}

// listAccounts lit passwd, lisible de tous. Un compte vient s'il est root ou
// un utilisateur (uid d'au moins 1000, sauf nobody), avec un nom sûr et un
// dossier personnel absolu et propre. Au-delà de MaxAccounts, le reste est dit
// par le second résultat.
func listAccounts(passwd io.Reader) ([]passwdEntry, bool) {
	content, err := io.ReadAll(io.LimitReader(passwd, maxPasswdBytes))
	if err != nil {
		return nil, false
	}
	var entries []passwdEntry
	names := map[string]bool{}
	for _, line := range strings.Split(string(content), "\n") {
		entry, ok := parsePasswdLine(line)
		if !ok || names[entry.name] {
			continue
		}
		if len(entries) == MaxAccounts {
			return entries, true
		}
		names[entry.name] = true
		entries = append(entries, entry)
	}
	return entries, false
}

func parsePasswdLine(line string) (passwdEntry, bool) {
	if len(line) > maxPasswdLine {
		return passwdEntry{}, false
	}
	fields := strings.Split(line, ":")
	if len(fields) < 6 {
		return passwdEntry{}, false
	}
	uid, err := strconv.ParseUint(fields[2], 10, 32)
	if err != nil {
		return passwdEntry{}, false
	}
	isRoot := uid == 0
	isUser := uid >= firstUser && uid != nobodyUID && uid != noUID
	home := fields[5]
	isCleanHome := filepath.IsAbs(home) && filepath.Clean(home) == home && home != "/"
	if (!isRoot && !isUser) || !IsAccountName(fields[0]) || !isCleanHome {
		return passwdEntry{}, false
	}
	return passwdEntry{name: fields[0], uid: uint32(uid), home: home}, true // #nosec G115 -- ParseUint(…, 32) borne déjà l'uid.
}

// readAccount lit les deux fichiers d'un compte. room est la place qui reste
// au rapport pour des clés.
func (r reader) readAccount(entry passwdEntry, room int) Account {
	account := Account{Name: entry.name, UID: int64(entry.uid), State: StateNone, Keys: []Key{}}
	dir, err := r.openHome(entry.home)
	if err != nil {
		account.State = stateOfError(err)
		return account
	}
	defer dir.Close()
	if state := r.checkSSHDirectory(dir, entry.uid); state != StateRead {
		account.State = state
		return account
	}
	states := make([]State, 0, len(Files()))
	for _, file := range Files() {
		result := r.readFile(dir, file, entry.uid)
		states = append(states, result.state)
		account.Ignored += result.ignored
		account.Keys = append(account.Keys, result.keys...)
	}
	account.State = combineStates(states)
	limit := min(MaxKeysPerAccount, room)
	if len(account.Keys) > limit {
		account.Keys = account.Keys[:limit]
		account.Truncated = true
	}
	return account
}

// checkSSHDirectory juge ~/.ssh : StateRead veut dire « on peut y lire ».
func (r reader) checkSSHDirectory(dir Dir, uid uint32) State {
	info, err := dir.Lstat(sshDirName)
	if err != nil {
		return stateOfError(err)
	}
	// Lstat ne suit pas un lien : un lien n'est pas un dossier.
	if !info.IsDir() || !r.isTrusted(info, uid) {
		return StateUnsafe
	}
	return StateRead
}

type fileResult struct {
	state   State
	keys    []Key
	ignored int
}

// readFile lit un authorized_keys, comme sshd (StrictModes) le prendrait : un
// fichier régulier, au compte ou à root, que ni le groupe ni les autres ne
// modifient. Il est jugé par Lstat avant d'être ouvert (un tube ouvert en
// lecture bloque), ouvert sans suivre de lien ni attendre, puis rejugé sur le
// descripteur : ce qui a été échangé entre-temps ne passe pas.
func (r reader) readFile(dir Dir, file string, uid uint32) fileResult {
	name := sshDirName + "/" + file
	info, err := dir.Lstat(name)
	if err != nil {
		return fileResult{state: stateOfError(err)}
	}
	if !info.Mode().IsRegular() || !r.isTrusted(info, uid) {
		return fileResult{state: StateUnsafe}
	}
	if info.Size() > MaxFileBytes {
		return fileResult{state: StateTooLarge}
	}
	handle, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return fileResult{state: stateOfError(err)}
		}
		return fileResult{state: StateUnsafe}
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !opened.Mode().IsRegular() || !r.isTrusted(opened, uid) {
		return fileResult{state: StateUnsafe}
	}
	content, err := io.ReadAll(io.LimitReader(handle, MaxFileBytes+1))
	if err != nil {
		return fileResult{state: StateDenied}
	}
	if len(content) > MaxFileBytes {
		return fileResult{state: StateTooLarge}
	}
	keys, ignored := parseKeys(file, content)
	return fileResult{state: StateRead, keys: keys, ignored: ignored}
}

func (r reader) isTrusted(info fs.FileInfo, accountUID uint32) bool {
	owner, known := r.ownerOf(info)
	if !known || (owner != accountUID && owner != 0) {
		return false
	}
	return info.Mode().Perm()&0o022 == 0
}

// stateOfError : rien là (ou pas de fichier, ou un « dossier » qui n'en est
// pas un) est « aucun », tout autre échec est un fichier que l'agent ne peut
// pas lire.
func stateOfError(err error) State {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return StateNone
	}
	return StateDenied
}

// combineStates résume les deux fichiers d'un compte par le plus grave :
// illisible, puis douteux, puis trop gros ; sinon lu s'il y en a un.
func combineStates(states []State) State {
	for _, worst := range []State{StateDenied, StateUnsafe, StateTooLarge, StateRead} {
		if slices.Contains(states, worst) {
			return worst
		}
	}
	return StateNone
}

// parseKeys extrait les clés d'un fichier lu. Une ligne vide ou en commentaire
// ne compte pas ; une ligne qui n'est pas une clé d'un type connu, ou trop
// longue, est comptée dans ignored. La même clé deux fois (même fichier)
// n'est qu'une, avec la réunion des noms d'option.
func parseKeys(file string, content []byte) ([]Key, int) {
	keys := []Key{}
	positions := map[string]int{}
	ignored := 0
	for _, line := range bytes.Split(content, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, ok := parseKeyLine(file, line)
		if !ok {
			ignored++
			continue
		}
		if position, seen := positions[key.Fingerprint]; seen {
			keys[position].Options = unionOptions(keys[position].Options, key.Options)
			continue
		}
		positions[key.Fingerprint] = len(keys)
		keys = append(keys, key)
	}
	return keys, ignored
}

func parseKeyLine(file string, line []byte) (Key, bool) {
	if len(line) > MaxLineBytes {
		return Key{}, false
	}
	public, comment, options, _, err := ssh.ParseAuthorizedKey(line)
	if err != nil || !slices.Contains(KeyTypes(), public.Type()) {
		return Key{}, false
	}
	return Key{
		File:        file,
		Fingerprint: ssh.FingerprintSHA256(public),
		Type:        public.Type(),
		Bits:        bitsOf(public),
		Comment:     CleanComment(comment),
		Options:     optionNames(options),
	}, true
}

// bitsOf : la longueur du module RSA, la courbe d'une clé ECDSA, 256 pour
// ed25519 et les clés de sécurité ; ssh-dss n'existe qu'en 1024.
func bitsOf(public ssh.PublicKey) int {
	if public.Type() == "ssh-dss" {
		return 1024
	}
	crypto, ok := public.(ssh.CryptoPublicKey)
	if !ok {
		return 256
	}
	switch key := crypto.CryptoPublicKey().(type) {
	case *rsa.PublicKey:
		return key.N.BitLen()
	case *ecdsa.PublicKey:
		return key.Curve.Params().BitSize
	case ed25519.PublicKey:
		return 256
	}
	return 0
}

// optionNames ne garde que le nom de chaque option, en minuscules, et « other »
// pour un nom que sshd(8) ne connaît pas : la valeur d'une option (une commande
// forcée) ne sort jamais de la machine, et un nom libre non plus.
func optionNames(options []string) []string {
	names := []string{}
	for _, option := range options {
		name, _, _ := strings.Cut(option, "=")
		name = strings.ToLower(name)
		if !slices.Contains(Options(), name) {
			name = OptionOther
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

func unionOptions(kept, added []string) []string {
	for _, name := range added {
		if !slices.Contains(kept, name) {
			kept = append(kept, name)
		}
	}
	return kept
}
