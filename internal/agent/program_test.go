package agent

import (
	"os"
	"syscall"
)

// writeProgram écrit un programme de test qu'on lancera ensuite. Pendant
// l'écriture, aucun fork du processus ne peut hériter du descripteur ouvert en
// écriture (syscall.ForkLock) : sinon un enfant, retardé par la charge, le
// tient encore quand le test lance le programme, et l'exec échoue en « text
// file busy » (restic_failed au lieu du résultat attendu).
func writeProgram(path string, content []byte, mode os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, content, mode) // #nosec G306 -- un programme de test, exécutable.
}
