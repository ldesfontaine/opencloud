package directory

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/registry"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// OfferedVersion rend le tag plus récent que le dernier constat de l'agent
// propose à cette unité, ou false s'il n'en propose pas. Le constat est celui
// du conteneur rattaché le plus récemment vérifié : un constat « à jour »
// tout frais l'emporte sur un ancien qui offrait. Une unité qui n'est pas
// suivie (hold, ignore) n'a pas d'offre : ses mises à jour ne se montrent pas.
// Une empreinte changée sous le même tag n'est pas une version, elle n'offre
// rien. Seuls comptent les conteneurs qui tournent l'image déclarée, et un tag
// qui n'a pas la forme d'une version (CheckVersion) n'est jamais offert.
func (d *Directory) OfferedVersion(ctx context.Context, unitID string) (string, bool, error) {
	unit, err := d.store.GetUnit(ctx, unitID)
	if err != nil {
		return "", false, err
	}
	if unit.Kind != UnitContainer {
		return "", false, ErrUnitNotContainer
	}
	if unit.UpdatePolicy != container.PolicyWatch {
		return "", false, nil
	}
	owner, err := d.store.GetDeclaredService(ctx, unit.ServiceID)
	if err != nil {
		return "", false, err
	}
	declared, err := registry.Parse(unit.Image)
	if err != nil {
		return "", false, nil
	}
	containers, checks, err := d.observed(ctx, owner.MachineID)
	if err != nil {
		return "", false, err
	}
	var latest update.Check
	found := false
	for _, item := range containers {
		if item.IsArchived() || item.UnitID != unitID || !sameRepository(item.Image, declared) {
			continue
		}
		check, ok := update.CheckFor(checks, item)
		if ok && (!found || check.CheckedAt.After(latest.CheckedAt)) {
			latest, found = check, true
		}
	}
	// Le tag vient du registre, par l'agent : il n'est offert que s'il a la
	// forme d'une version que l'annuaire accepterait.
	if !found || CheckVersion(latest.NewerTag) != nil {
		return "", false, nil
	}
	return latest.NewerTag, true, nil
}

// sameRepository : le conteneur tourne l'image que l'unité déclare (même
// registre, même dépôt). Le tag plus récent d'une autre image, rattachée par
// erreur, ne se pose pas sur celle de l'unité.
func sameRepository(image string, declared registry.Reference) bool {
	observed, err := registry.Parse(image)
	return err == nil && observed.Registry == declared.Registry && observed.Repository == declared.Repository
}

// SetUnitVersion change la version déclarée d'une unité conteneur. Elle ne
// tire ni ne redémarre rien : l'appelant redéploie ensuite. Le rapprochement
// est refait, comme après toute écriture de l'annuaire.
func (d *Directory) SetUnitVersion(ctx context.Context, unitID, version string) error {
	unit, err := d.store.GetUnit(ctx, unitID)
	if err != nil {
		return err
	}
	if unit.Kind != UnitContainer {
		return ErrUnitNotContainer
	}
	if err := CheckVersion(version); err != nil {
		return err
	}
	owner, err := d.store.GetDeclaredService(ctx, unit.ServiceID)
	if err != nil {
		return err
	}
	if err := d.store.SetUnitVersion(ctx, unitID, version, d.now()); err != nil {
		return err
	}
	d.logger.Info("unit version set", "service_id", owner.ID, "unit_id", unitID, "from", unit.Version, "to", version)
	d.auditor.Record(ctx, unitVersionEvent(unit, owner, version))
	d.settle(ctx, owner.MachineID)
	d.changed()
	return nil
}
