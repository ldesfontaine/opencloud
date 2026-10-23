package deploy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/directory"
)

// Render écrit le compose.yml d'un service déclaré, et rend les noms des
// unités qui montent un dossier de données : l'agent crée data/<nom> pour
// chacune. Le fichier ne dit que ce que l'annuaire sait (l'image épinglée,
// les ports, un dossier de données, le rattachement) : ni réseau, ni
// privilège, ni build. L'écriture est celle d'agentwire.RenderCompose, que
// l'agent refait pour vérifier le fichier avant de l'écrire ; chaque valeur
// y est rejugée, même si l'annuaire l'a déjà fait. Même annuaire, même
// fichier, octet pour octet.
func Render(service directory.Service) (compose []byte, dataDirs []string, err error) {
	if err := check(service); err != nil {
		return nil, nil, err
	}
	units := make([]agentwire.ComposeUnit, 0, len(service.Units))
	for _, unit := range service.Units {
		units = append(units, agentwire.ComposeUnit{Name: unit.Name, Image: imageOf(unit), Ports: unit.Ports, DataPath: unit.DataPath})
	}
	compose, dataDirs, err = agentwire.RenderCompose(service.ComposeProject, service.ID, units)
	if err != nil {
		return nil, nil, refusalOf(err)
	}
	return compose, dataDirs, nil
}

// refusalOf dit, avec la clé de l'annuaire, quelle valeur ne s'écrit pas
// dans le fichier : l'opérateur la corrige dans la fiche du service.
func refusalOf(err error) error {
	switch {
	case errors.Is(err, agentwire.ErrComposeProject):
		return directory.ErrComposeProjectInvalid
	case errors.Is(err, agentwire.ErrComposeUnits):
		return directory.ErrUnitsInvalid
	case errors.Is(err, agentwire.ErrComposeUnitName):
		return directory.ErrUnitNameInvalid
	case errors.Is(err, agentwire.ErrComposeImage):
		return directory.ErrImageInvalid
	case errors.Is(err, agentwire.ErrComposePort):
		return directory.ErrPortsInvalid
	case errors.Is(err, agentwire.ErrComposeDataPath):
		return directory.ErrDataPathInvalid
	case errors.Is(err, agentwire.ErrComposeTooLarge):
		return ErrComposeTooLarge
	}
	return fmt.Errorf("render compose file: %w", err)
}

// imageOf écrit la référence épinglée : image:version, puis @sha256:… quand
// l'empreinte est déclarée.
func imageOf(unit directory.Unit) string {
	reference := unit.Image + ":" + unit.Version
	if unit.Digest != "" {
		reference += "@" + unit.Digest
	}
	return reference
}

// check dit si le service se déploie : ce que l'annuaire doit porter pour que
// le fichier soit complet. Chaque refus a sa clé, que la fiche traduit.
func check(service directory.Service) error {
	switch {
	case service.Origin != directory.OriginCompose:
		return ErrOriginNotCompose
	case service.ComposeProject == "":
		return ErrProjectMissing
	case len(service.Units) == 0:
		return ErrNoUnits
	}
	for _, unit := range service.Units {
		if unit.Kind != directory.UnitContainer {
			return ErrUnitNotContainer
		}
		// latest est déjà refusé à la déclaration ; ici, c'est l'absence.
		if strings.TrimSpace(unit.Version) == "" {
			return ErrVersionUnpinned
		}
	}
	return nil
}
