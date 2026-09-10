package catalog

// Service porte le catalogue sous la forme que runner et web consomment : une
// valeur, pas un package. Le catalogue est figé au build ; la seule dépendance
// est la source des jetons de zone, que « Poser le jeton DNS » dépose.
type Service struct {
	Tokens Tokens
}

func (Service) Definitions() []Definition {
	return Definitions()
}

func (Service) Lookup(kind Kind) (Definition, bool) {
	return Lookup(kind)
}

func (s Service) Prepare(kind Kind, params map[string]string) (Prepared, error) {
	return prepare(kind, params, s.Tokens)
}
