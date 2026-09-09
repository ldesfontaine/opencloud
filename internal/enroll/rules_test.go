package enroll

import (
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/scripts"
)

// La séquence d'enrôlement est écrite une seule fois, dans le script de
// l'action Enrôler : la machine openCloud le joue en root sur elle-même, une
// machine distante le reçoit collé. Ces tests tiennent ce que le texte du
// script doit dire — tout changement les casse, c'est voulu.

func enrolerScript(t *testing.T) string {
	t.Helper()
	script, err := scripts.Script("enroler")
	if err != nil {
		t.Fatalf("assembler le script d'enrôlement : %v", err)
	}
	return string(script)
}

func TestScriptEnroler_LaRegleSudoNOuvreQueLeLanceur(t *testing.T) {
	rule, found := heredoc(enrolerScript(t), "SUDOERS")
	if !found {
		t.Fatal("le script ne porte pas de bloc « SUDOERS »")
	}

	lines := strings.Split(strings.TrimSpace(rule), "\n")
	if len(lines) != 3 {
		t.Fatalf("la règle sudo tient en trois lignes ; celle-ci en fait %d :\n%s", len(lines), rule)
	}
	if !strings.HasPrefix(lines[0], "Defaults:opencloud env_reset") {
		t.Errorf("l'environnement du compte doit être remis à zéro : %q", lines[0])
	}
	allowed := map[string]bool{
		"opencloud ALL=(root) NOPASSWD: /usr/local/sbin/oc-launch": true,
		"opencloud ALL=(root) NOPASSWD: /usr/bin/install -o root -g root -m 0755 " +
			"/var/lib/opencloud/oc-launch.new /usr/local/sbin/oc-launch": true,
	}
	for _, line := range lines[1:] {
		if !allowed[line] {
			t.Errorf("la règle sudo ouvre autre chose que le lanceur et sa pose : %q", line)
		}
	}
	if strings.Contains(rule, "*") {
		t.Errorf("la règle sudo porte un joker :\n%s", rule)
	}
}

func TestScriptEnroler_LeDropInSshdRefermeSonBloc(t *testing.T) {
	dropIn, found := heredoc(enrolerScript(t), "SSHD_DROP_IN")
	if !found {
		t.Fatal("le script ne porte pas de bloc « SSHD_DROP_IN »")
	}

	if !strings.Contains(dropIn, "Match User opencloud\n") {
		t.Errorf("le drop-in ne vaut pas pour le seul compte de service :\n%s", dropIn)
	}
	// Sans « Match all », tout ce qui suit l'Include dans sshd_config ne
	// vaudrait plus que pour le compte opencloud.
	if !strings.HasSuffix(strings.TrimSpace(dropIn), "Match all") {
		t.Errorf("le drop-in ne referme pas son bloc :\n%s", dropIn)
	}
	for _, directive := range []string{
		"AuthenticationMethods publickey",
		"PasswordAuthentication no",
		"PermitTTY no",
		"X11Forwarding no",
		"AllowAgentForwarding no",
		"AllowTcpForwarding no",
		"PermitTunnel no",
	} {
		if !strings.Contains(dropIn, directive+"\n") {
			t.Errorf("le drop-in ne porte pas « %s » :\n%s", directive, dropIn)
		}
	}
}

// L'ordre est une propriété de sécurité (15-catalogue-actions.md §3) : rien
// n'est écrit avant le préflight, et la clé arrive en dernier — tant qu'elle
// n'est pas là, personne ne peut se servir de ce que le reste a ouvert.
func TestScriptEnroler_LOrdreDesEtapesEstUneProprieteDeSecurite(t *testing.T) {
	script := enrolerScript(t)

	sequence := []string{
		`step "préflight`,
		`step "le compte`,
		`step "la règle sudo`,
		`step "le drop-in sshd`,
		`step "la clé autorisée`,
	}
	previous := -1
	for _, marker := range sequence {
		at := strings.Index(script, marker)
		if at < 0 {
			t.Fatalf("le script ne porte pas l'étape « %s »", marker)
		}
		if at < previous {
			t.Errorf("l'étape « %s » vient trop tôt dans le script", marker)
		}
		previous = at
	}

	// sshd -t exige /run/sshd ; sur une machine où sshd n'a jamais tourné, il
	// manque, et la validation échouerait pour cette seule raison.
	if strings.Index(script, "mkdir -p -- /run/sshd") > strings.Index(script, "$(sshd -t") {
		t.Error("le script valide la configuration de sshd avant de poser /run/sshd")
	}
}

func TestScriptEnroler_VerifieLesBinairesDontLaSequenceDepend(t *testing.T) {
	script := enrolerScript(t)

	for _, binary := range []string{
		"/usr/bin/sudo", "/usr/sbin/visudo", "/usr/sbin/sshd", "/usr/sbin/useradd",
		"/usr/sbin/usermod", "/usr/bin/passwd", "/usr/bin/install", "/usr/bin/ssh-keygen",
	} {
		if !strings.Contains(script, "require_binary "+binary+" ") {
			t.Errorf("le préflight du script ne vérifie pas %s", binary)
		}
	}
}

// Un refus n'est jamais muet : la cause, puis le geste qui la lève
// (15-catalogue-actions.md §1).
func TestScriptEnroler_ChaqueRefusNommeSaCauseEtSonRemede(t *testing.T) {
	lines := strings.Split(enrolerScript(t), "\n")

	refusals := 0
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, `refuse "`) {
			continue
		}
		refusals++
		if !strings.HasSuffix(trimmed, `\`) || index+1 >= len(lines) {
			t.Errorf("ligne %d : ce refus ne porte pas de remède : %s", index+1, trimmed)
			continue
		}
		if remedy := strings.TrimSpace(lines[index+1]); !strings.HasPrefix(remedy, `"`) {
			t.Errorf("ligne %d : le remède attendu est absent : %s", index+2, remedy)
		}
	}
	if refusals == 0 {
		t.Fatal("le script ne refuse jamais rien : le préflight a disparu")
	}
}

// heredoc rend le corps d'un « cat << 'DELIMITEUR' » du script.
func heredoc(script, delimiter string) (string, bool) {
	_, after, found := strings.Cut(script, "<< '"+delimiter+"'\n")
	if !found {
		return "", false
	}
	body, _, found := strings.Cut(after, "\n"+delimiter+"\n")
	if !found {
		return "", false
	}
	return body + "\n", true
}
