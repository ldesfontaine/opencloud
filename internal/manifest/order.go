package manifest

import (
	"slices"
	"strings"
)

// keyedStep est un changement de projet ou de service, avec les clés
// d'unicité qu'il tenait avant et celles qu'il tiendra après : un nom, un
// projet Compose, la clé d'observation d'une unité.
type keyedStep struct {
	step  step
	label string
	line  int
	holds []string
	wants []string
}

// orderKeyed range les modifications dans un ordre qui libère une clé avant
// qu'une autre la prenne, puis les créations, qui ne libèrent rien :
//
//   - un service qui passe à un nom qu'un autre quitte dans le même fichier
//     passe après lui ;
//   - deux services qui se prennent la clé l'un de l'autre (un échange de
//     noms) n'ont pas d'ordre : swap_refused, « en deux fois » ;
//   - une clé qu'un objet supprimé par --prune tient encore ne peut pas être
//     reprise dans le même passage, la suppression venant en dernier :
//     key_from_pruned.
//
// Les modifications gardent l'ordre du fichier hors ces dépendances.
func (b *builder) orderKeyed(updates, creates []keyedStep, prunedKeys map[string]bool) []step {
	for _, change := range slices.Concat(updates, creates) {
		for _, key := range change.wants {
			if !slices.Contains(change.holds, key) && prunedKeys[key] {
				b.refuse(KeyKeyFromPruned, change.line, change.label, keyText(key))
			}
		}
	}
	releasedBy := map[string]int{}
	for index, change := range updates {
		for _, key := range change.holds {
			if !slices.Contains(change.wants, key) {
				releasedBy[key] = index
			}
		}
	}
	// after[i] liste ce qui doit suivre i ; waiting[j] compte ce que j attend.
	after := make([][]int, len(updates))
	waiting := make([]int, len(updates))
	for index, change := range updates {
		for _, key := range change.wants {
			if slices.Contains(change.holds, key) {
				continue
			}
			if releaser, found := releasedBy[key]; found && releaser != index && !slices.Contains(after[releaser], index) {
				after[releaser] = append(after[releaser], index)
				waiting[index]++
			}
		}
	}
	ordered := make([]step, 0, len(updates)+len(creates))
	done := make([]bool, len(updates))
	for len(ordered) < len(updates) {
		next := -1
		for index := range updates {
			if !done[index] && waiting[index] == 0 {
				next = index
				break
			}
		}
		if next < 0 {
			b.refuseSwap(updates, done)
			// Le reste est rendu dans l'ordre du fichier : le plan est refusé.
			for index := range updates {
				if !done[index] {
					ordered = append(ordered, updates[index].step)
					done[index] = true
				}
			}
			break
		}
		done[next] = true
		ordered = append(ordered, updates[next].step)
		for _, follower := range after[next] {
			waiting[follower]--
		}
	}
	for _, change := range creates {
		ordered = append(ordered, change.step)
	}
	return ordered
}

// refuseSwap nomme les changements qui s'attendent les uns les autres.
func (b *builder) refuseSwap(updates []keyedStep, done []bool) {
	var labels []string
	for index, change := range updates {
		if !done[index] {
			labels = append(labels, change.label)
		}
	}
	b.refuse(KeySwapRefused, 0, "", joinLabels(labels))
}

// keyText écrit une clé d'unicité pour l'opérateur : sans le préfixe de sa
// règle ni la machine, qui est un identifiant interne.
func keyText(key string) string {
	_, rest, _ := strings.Cut(key, ":")
	if _, afterMachine, found := strings.Cut(rest, "/"); found {
		return afterMachine
	}
	return rest
}

// keyHolder est celui qui tiendra une clé d'unicité dans l'état visé.
type keyHolder struct {
	label    string
	line     int
	fromFile bool
}

// keyHolders range, par clé, ce qui la tiendra une fois le fichier appliqué.
// Deux porteurs, c'est un conflit que la base refuserait à l'écriture : le
// plan le dit avant.
type keyHolders map[string][]keyHolder

func (h keyHolders) claim(key string, holder keyHolder) {
	h[key] = append(h[key], holder)
}

// claimAll prend chaque clé une fois : deux unités du même service qui se
// partagent une clé sont l'affaire de la validation du service.
func (h keyHolders) claimAll(keys []string, holder keyHolder) {
	seen := map[string]bool{}
	for _, key := range keys {
		if !seen[key] {
			seen[key] = true
			h.claim(key, holder)
		}
	}
}

// report note un refus par conflit, à la ligne de l'entrée du fichier qui le
// porte. Deux entrées du fichier de même nom sont déjà refusées par la
// reconnaissance (name_duplicate) : pas deux fois.
func (h keyHolders) report(b *builder, errorFor func(key string) error) {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		holders := h[key]
		if len(holders) < 2 {
			continue
		}
		allFromFile := true
		for _, holder := range holders {
			allFromFile = allFromFile && holder.fromFile
		}
		nameKey := strings.HasPrefix(key, "name:") || strings.HasPrefix(key, "project:")
		if allFromFile && nameKey {
			continue
		}
		targets := holders
		if allFromFile {
			targets = holders[1:]
		}
		for _, holder := range targets {
			if holder.fromFile {
				b.refuseError(errorFor(key), holder.line, holder.label)
			}
		}
	}
}
