package alert

import "slices"

// Category regroupe les types d'alerte qu'un canal peut choisir de recevoir.
// Un canal sans catégorie reçoit tout.
type Category string

const (
	CategoryMachines Category = "machines"
	CategoryServices Category = "services"
	CategoryJobs     Category = "jobs"
	CategoryProbes   Category = "probes"
	CategoryBackups  Category = "backups"
	CategoryUpdates  Category = "updates"
)

// Categories liste le catalogue, dans l'ordre des écrans.
func Categories() []Category {
	return []Category{CategoryMachines, CategoryServices, CategoryJobs, CategoryProbes, CategoryBackups, CategoryUpdates}
}

func isCategory(category Category) bool {
	return slices.Contains(Categories(), category)
}

// categoryOfKind est la seule table qui range un type d'alerte : un nouveau
// type s'y ajoute en une ligne, et le test « chaque type a une catégorie »
// échoue tant qu'elle manque.
var categoryOfKind = map[Kind]Category{
	KindMachineLost:       CategoryMachines,
	KindDiskFull:          CategoryMachines,
	KindCPUHigh:           CategoryMachines,
	KindMemoryHigh:        CategoryMachines,
	KindServiceDown:       CategoryServices,
	KindServiceUnhealthy:  CategoryServices,
	KindServiceRestart:    CategoryServices,
	KindJobLate:           CategoryJobs,
	KindJobFailed:         CategoryJobs,
	KindProbeDown:         CategoryProbes,
	KindCertExpiring:      CategoryProbes,
	KindCertExpired:       CategoryProbes,
	KindCertUntrusted:     CategoryProbes,
	KindBackupStale:       CategoryBackups,
	KindRestoreTestStale:  CategoryBackups,
	KindSourceUnprotected: CategoryBackups,
	KindTargetFull:        CategoryBackups,
	KindTargetUnreachable: CategoryBackups,
	KindAgentRolledBack:   CategoryUpdates,
}

// CategoryOf rend la catégorie d'un type d'alerte ; un type inconnu n'en a
// pas, et ne passe donc que par un canal sans filtre.
func CategoryOf(kind Kind) (Category, bool) {
	category, found := categoryOfKind[kind]
	return category, found
}

// wantsCategory : liste vide, tout passe ; sinon la catégorie de l'alerte
// doit y figurer.
func wantsCategory(categories []Category, kind Kind) bool {
	if len(categories) == 0 {
		return true
	}
	category, found := CategoryOf(kind)
	return found && slices.Contains(categories, category)
}

// normalizedCategories écarte les doublons et range selon le catalogue ;
// un nom inconnu est gardé, en fin de liste, pour que la validation le
// refuse au lieu de le perdre en silence. Rend nil pour « tout ».
func normalizedCategories(categories []Category) []Category {
	var kept []Category
	for _, category := range categories {
		if !slices.Contains(kept, category) {
			kept = append(kept, category)
		}
	}
	slices.SortStableFunc(kept, func(a, b Category) int {
		return categoryRank(a) - categoryRank(b)
	})
	return kept
}

func categoryRank(category Category) int {
	if index := slices.Index(Categories(), category); index >= 0 {
		return index
	}
	return len(Categories())
}
