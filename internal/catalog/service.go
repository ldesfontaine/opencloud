package catalog

// Service porte le catalogue sous la forme que runner et web consomment : une
// valeur, pas un package. Il n'a pas d'état, le catalogue est figé au build.
type Service struct{}

func (Service) Definitions() []Definition {
	return Definitions()
}

func (Service) Lookup(kind Kind) (Definition, bool) {
	return Lookup(kind)
}

func (Service) Prepare(kind Kind, params map[string]string) (Prepared, error) {
	return Prepare(kind, params)
}
