package version

// Seules variables de package du projet : ldflags ne sait écrire que là.
var (
	number = "v0.0.1"
	commit = ""
)

func Number() string {
	return number
}

// Commit est le commit du binaire ; vide hors d'un binaire construit par make.
func Commit() string {
	return commit
}

func String() string {
	if commit == "" {
		return number
	}
	return number + " (" + commit + ")"
}
