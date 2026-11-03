package directory

import (
	"cmp"
	"context"
	"slices"

	"github.com/ldesfontaine/opencloud/internal/link"
)

// ErrNotDeduced : l'identifiant ne désigne aucune route déduite aujourd'hui
// (le service a perdu son adresse publique, le proxy n'existe plus, ou la
// route a déjà été confirmée ou écartée).
var ErrNotDeduced = link.ErrNotFound

// DeduceRoutes calcule les routes publiques que les services déclarés
// laissent supposer : un service qui a une adresse publique est joint par
// chaque service qui fournit la capacité proxy. C'est un repli faible : le
// proxy peut en ignorer certains, et rien ne dit lequel route quoi quand il y
// en a plusieurs ; l'opérateur confirme ou écarte.
//
// Chaque côté est la première unité qui sait dire sa clé. La paire, déjà
// connue de `known` (confirmée, écrite à la main ou écartée), n'est pas
// rendue : un refus ne revient pas par surprise.
func DeduceRoutes(services []Service, known map[link.Endpoint]map[link.Endpoint]bool) []link.Link {
	var proxies []link.Endpoint
	for _, owner := range services {
		if slices.Contains(owner.Provides, CapabilityProxy) {
			if endpoint, found := firstEndpoint(owner); found {
				proxies = append(proxies, endpoint)
			}
		}
	}
	var deduced []link.Link
	for _, owner := range services {
		if owner.PublicURL == "" {
			continue
		}
		target, found := firstEndpoint(owner)
		if !found {
			continue
		}
		for _, proxy := range proxies {
			if proxy == target || known[proxy][target] {
				continue
			}
			deduced = append(deduced, link.Link{
				ID: link.DeducedID(proxy, target, link.NatureRoute), From: proxy, To: target,
				Nature: link.NatureRoute, Proof: link.ProofDeduced, Signal: link.SignalPublicURL,
			})
		}
	}
	slices.SortFunc(deduced, func(a, b link.Link) int { return cmp.Compare(a.ID, b.ID) })
	return deduced
}

func firstEndpoint(owner Service) (link.Endpoint, bool) {
	for _, unit := range owner.Units {
		if key := UnitKey(owner, unit); key != "" {
			return link.Endpoint{MachineID: owner.MachineID, Key: key}, true
		}
	}
	return link.Endpoint{}, false
}

// knownRoutes range les routes déjà décidées, par paire : écrites (déclarées)
// ou écartées. Les autres natures n'effacent pas une route.
func knownRoutes(stored, dismissed []link.Link) map[link.Endpoint]map[link.Endpoint]bool {
	known := map[link.Endpoint]map[link.Endpoint]bool{}
	for _, found := range slices.Concat(stored, dismissed) {
		if found.Nature != link.NatureRoute {
			continue
		}
		if known[found.From] == nil {
			known[found.From] = map[link.Endpoint]bool{}
		}
		known[found.From][found.To] = true
	}
	return known
}

// deducedRoutes lit les liens et rend les routes à proposer pour ces services.
func (d *Directory) deducedRoutes(ctx context.Context, declared []Service) ([]link.Link, error) {
	stored, err := d.linker.List(ctx, "")
	if err != nil {
		return nil, err
	}
	dismissed, err := d.linker.ListDismissed(ctx, "")
	if err != nil {
		return nil, err
	}
	return DeduceRoutes(declared, knownRoutes(stored, dismissed)), nil
}

// ConfirmLink fait d'une route déduite un lien déclaré.
func (d *Directory) ConfirmLink(ctx context.Context, id string) error {
	found, err := d.findDeduced(ctx, id)
	if err != nil {
		return err
	}
	_, err = d.linker.ConfirmRoute(ctx, link.Definition{From: found.From, To: found.To})
	return err
}

// DismissLink écarte une route déduite : elle ne se propose plus.
func (d *Directory) DismissLink(ctx context.Context, id string) error {
	found, err := d.findDeduced(ctx, id)
	if err != nil {
		return err
	}
	_, err = d.linker.DismissRoute(ctx, link.Definition{From: found.From, To: found.To})
	return err
}

// RestoreLink rend à la déduction une route écartée.
func (d *Directory) RestoreLink(ctx context.Context, id string) error {
	if d.linker == nil {
		return errNoLinker
	}
	return d.linker.Restore(ctx, id)
}

func (d *Directory) findDeduced(ctx context.Context, id string) (link.Link, error) {
	if d.linker == nil {
		return link.Link{}, errNoLinker
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{})
	if err != nil {
		return link.Link{}, err
	}
	deduced, err := d.deducedRoutes(ctx, declared)
	if err != nil {
		return link.Link{}, err
	}
	index := slices.IndexFunc(deduced, func(found link.Link) bool { return found.ID == id })
	if index < 0 {
		return link.Link{}, ErrNotDeduced
	}
	return deduced[index], nil
}
