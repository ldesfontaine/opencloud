package rollback

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Le vrai lanceur de processus, avec de petits scripts de test : rien en
// production ne lance de shell, c'est ici le binaire factice.
func script(t *testing.T, body string) string {
	t.Helper()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, []byte("#!"+shell+"\n"+body+"\n"), 0o700); err != nil { // #nosec G306 -- un script de test exécutable
		t.Fatal(err)
	}
	return path
}

func TestExecLauncher_PassesTheExitCodeAndTheGuardEnvironment(t *testing.T) {
	witness := filepath.Join(t.TempDir(), "env")
	path := script(t, `printf '%s' "$OPENCLOUD_AGENT_GUARD" > `+witness+`; exit 3`)
	child, err := ExecLauncher(path, nil, []string{"OPENCLOUD_AGENT_GUARD=1"})
	if err != nil {
		t.Fatal(err)
	}
	if exit := child.Wait(); exit.Code != 3 {
		t.Fatalf("exit %+v", exit)
	}
	if content, err := os.ReadFile(witness); err != nil || string(content) != "1" { // #nosec G304 -- le témoin du test.
		t.Fatalf("environment: %q %v", content, err)
	}
}

func TestExecLauncher_ABinaryThatCannotStartIsAnErrorNotAnExit(t *testing.T) {
	if _, err := ExecLauncher(filepath.Join(t.TempDir(), "absent"), nil, nil); err == nil {
		t.Fatal("a missing binary started")
	}
	notExecutable := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(notExecutable, []byte("not a binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecLauncher(notExecutable, nil, nil); err == nil {
		t.Fatal("a file that is not executable started")
	}
}

func TestExecLauncher_StopAsksTheBinaryToEnd(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	path := script(t, `trap 'exit 0' TERM; : > `+ready+`; while :; do sleep 0.05; done`)
	child, err := ExecLauncher(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Exit, 1)
	go func() { done <- child.Wait() }()
	// Le piège est armé quand le script l'a dit : le test n'attend pas à l'aveugle.
	waitFor(t, "the script is ready", func() bool {
		_, err := os.Stat(ready)
		return err == nil
	})
	child.Stop()
	select {
	case exit := <-done:
		if exit.Code != 0 {
			t.Fatalf("exit %+v", exit)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the binary never ended")
	}
}
