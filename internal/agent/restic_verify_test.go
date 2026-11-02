package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const (
	secretFileName   = "/home/alice/finances-2025.ods"
	snapshotOlder    = "1111111111111111111111111111111111111111111111111111111111111111"
	snapshotNewer    = "2222222222222222222222222222222222222222222222222222222222222222"
	verifyDumpedText = "contenu du fichier relu"
)

// fakeVerifyBinary écrit un faux restic qui répond aux quatre commandes de la
// vérification et note ce qu'il a reçu. mode : « ok », « check-fails »
// (check sort 1 en recopiant le mot de passe), « empty » (aucun instantané),
// « dump-fails » (dump sort 1) ou « missing » (check sort 10).
func fakeVerifyBinary(t *testing.T, mode string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' "$@" >> ` + dir + `/args
env > ` + dir + `/env
cp "$RESTIC_PASSWORD_FILE" ` + dir + `/password
stat -c %a "$RESTIC_PASSWORD_FILE" > ` + dir + `/mode
printf '%s' "$RESTIC_PASSWORD_FILE" > ` + dir + `/path
mode="` + mode + `"
case "$4" in
check)
  [ "$mode" = missing ] && exit 10
  if [ "$mode" = check-fails ]; then echo "Fatal: pack abcd damaged, password $(cat "$RESTIC_PASSWORD_FILE")" >&2; exit 1; fi
  echo "no errors were found"; exit 0 ;;
snapshots)
  if [ "$mode" = empty ]; then echo '[]'; exit 0; fi
  echo '[{"id":"` + snapshotOlder + `","time":"2026-10-30T02:00:00Z"},{"id":"` + snapshotNewer + `","time":"2026-11-01T02:00:00Z"}]'; exit 0 ;;
ls)
  echo '{"struct_type":"snapshot","id":"x"}'
  echo '{"struct_type":"node","type":"dir","path":"/home"}'
  echo '{"struct_type":"node","type":"file","path":"` + secretFileName + `","size":23}'
  echo '{"struct_type":"node","type":"file","path":"/home/alice/empty","size":0}'
  echo '{"struct_type":"node","type":"file","path":"/srv/big.tar","size":900}'
  exit 0 ;;
dump)
  [ "$mode" = dump-fails ] && exit 1
  case "$*" in *big.tar*) head -c 900 /dev/zero ;; *) printf '` + verifyDumpedText + `' ;; esac
  exit 0 ;;
esac
exit 2
`
	binary := filepath.Join(dir, "restic")
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary, dir
}

func verifyBench(t *testing.T, mode string) (*targetBench, string) {
	t.Helper()
	b := newTargetBench(t, "restic.verify")
	expectResult(t, b.run(t, agentwire.KindResticTargetSetup, setupParams()), agentwire.ActionSucceeded, "")
	binary, noted := fakeVerifyBinary(t, mode)
	b.runner.checks.findRestic = func() (string, error) { return binary, nil }
	return b, noted
}

func (b *targetBench) verify(t *testing.T) []agentwire.ActionResult {
	t.Helper()
	return b.runWithSecret(t, agentwire.KindResticVerify, agentwire.ResticVerifyParams{User: resticUser}, agentwire.ResticVerifySecret{RepoPassword: seededRepoPassword})
}

// restic.verify lance check, liste les derniers instantanés, puis relit dans
// chacun le plus petit fichier non vide et le plus gros. Le serveur reçoit
// des comptes ; le mot de passe est un fichier 0600 effacé ensuite, et ni un
// nom de fichier ni un octet du contenu ne remonte.
func TestResticVerify_ChecksThenReadsASampleAndReportsOnlyCounts(t *testing.T) {
	b, noted := verifyBench(t, "ok")

	result := expectResult(t, b.verify(t), agentwire.ActionSucceeded, "")

	if result.Verify == nil || *result.Verify != (agentwire.ResticVerifyReport{Snapshots: 2, Files: 4, Bytes: 2 * (23 + 900)}) {
		t.Fatalf("report %+v", result.Verify)
	}
	args := readNoted(t, noted, "args")
	for _, want := range []string{"check", "--read-data-subset", "snapshots", "--latest", "ls", "dump", snapshotNewer} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, seededRepoPassword) || strings.Contains(readNoted(t, noted, "env"), seededRepoPassword) {
		t.Fatal("the password reached an argument or the environment")
	}
	if readNoted(t, noted, "password") != seededRepoPassword || strings.TrimSpace(readNoted(t, noted, "mode")) != "600" {
		t.Fatal("restic did not read the password from a private file")
	}
	if _, err := os.Stat(readNoted(t, noted, "path")); !os.IsNotExist(err) {
		t.Fatal("the password file outlived the order")
	}
	everything := b.logs.String()
	for _, line := range result.Output {
		everything += line.Line + "\n"
	}
	for _, leaked := range []string{seededRepoPassword, secretFileName, "finances", verifyDumpedText} {
		if strings.Contains(everything, leaked) {
			t.Fatalf("%q reached the result or the journal:\n%s", leaked, everything)
		}
	}
}

func TestResticVerify_AFailedCheckIsReportedWithoutThePassword(t *testing.T) {
	b, noted := verifyBench(t, "check-fails")
	result := expectResult(t, b.verify(t), agentwire.ActionFailed, agentwire.ReasonCheckFailed)
	if result.Verify != nil {
		t.Fatal("a failed check reports counts")
	}
	for _, line := range result.Output {
		if strings.Contains(line.Line, seededRepoPassword) {
			t.Fatalf("the password reached the output: %q", line.Line)
		}
	}
	if _, err := os.Stat(readNoted(t, noted, "path")); !os.IsNotExist(err) {
		t.Fatal("the password file outlived a failed order")
	}
}

func TestResticVerify_NamesWhatWentWrong(t *testing.T) {
	cases := map[string]string{
		"empty":      agentwire.ReasonRepoEmpty,
		"missing":    agentwire.ReasonRepoEmpty,
		"dump-fails": agentwire.ReasonRestoreFailed,
	}
	for mode, reason := range cases {
		b, _ := verifyBench(t, mode)
		expectResult(t, b.verify(t), agentwire.ActionFailed, reason)
	}
}

func TestResticVerify_RefusesWithoutSecretOrWithoutRestic(t *testing.T) {
	b, noted := verifyBench(t, "ok")
	expectResult(t, b.run(t, agentwire.KindResticVerify, agentwire.ResticVerifyParams{User: resticUser}), agentwire.ActionFailed, agentwire.ReasonBadOrder)
	b.runner.checks.findRestic = func() (string, error) { return "", os.ErrNotExist }
	expectResult(t, b.verify(t), agentwire.ActionRefused, agentwire.ReasonResticMissing)
	if _, err := os.Stat(filepath.Join(noted, "args")); !os.IsNotExist(err) {
		t.Fatal("restic ran for a refused order")
	}
}

// Le droit restic.verify se juge à l'ordre : une cible qui ne l'a pas refuse.
func TestResticVerify_NeedsItsRight(t *testing.T) {
	b := newTargetBench(t)
	expectResult(t, b.verify(t), agentwire.ActionRefused, agentwire.RightMissing("restic.verify"))
}
