package enroll

import (
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/scripts"
)

// La machine openCloud s'enrôle en Go, une machine distante par le script :
// les deux chemins posent les mêmes fichiers, et le texte vit à deux endroits.
// Ce test est ce qui empêche les deux de diverger sans qu'on le voie.
func TestScriptEnroler_PoseLesMemesReglesQueLAmorcageEnGo(t *testing.T) {
	script, err := scripts.Script("enroler")
	if err != nil {
		t.Fatalf("assembler le script d'enrôlement : %v", err)
	}

	cases := map[string]struct {
		delimiter string
		expected  string
	}{
		"règle sudo":   {delimiter: "SUDOERS", expected: sudoersContent},
		"drop-in sshd": {delimiter: "SSHD_DROP_IN", expected: sshdDropInContent},
	}
	for name, testCase := range cases {
		got, found := heredoc(string(script), testCase.delimiter)
		if !found {
			t.Errorf("%s : le script ne porte pas de bloc « %s »", name, testCase.delimiter)
			continue
		}
		if got != testCase.expected {
			t.Errorf("%s, le script dit :\n%s\nle Go dit :\n%s", name, got, testCase.expected)
		}
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
