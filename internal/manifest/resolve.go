package manifest

import (
	"strconv"
)

// Ce que match rend pour une entrée du fichier.
const (
	// createEntry : aucun objet de la base ne correspond, l'entrée crée.
	createEntry = -1
	// refusedEntry : l'entrée est refusée, le reste de son plan l'ignore.
	refusedEntry = -2
)

// entryRef est ce que la reconnaissance lit d'une entrée du fichier.
type entryRef struct {
	id   string
	name string
	line int
}

// objectRef est ce qu'elle lit d'un objet de la base.
type objectRef struct {
	id   string
	name string
}

// matching dit, pour chaque entrée, l'objet de la base qu'elle désigne.
type matching struct {
	// existing[i] est l'indice, dans les objets de la base, de l'objet de
	// l'entrée i ; createEntry ou refusedEntry sinon.
	existing []int
	// claimed[j] dit qu'une entrée désigne l'objet j de la base.
	claimed []bool
}

// match retrouve l'objet de chaque entrée (resolve) :
//
//  1. un id donné deux fois est refusé, avec les deux lignes ;
//  2. un id présent désigne son objet, et un id inconnu est refusé : une
//     coquille ou un fichier d'une autre base ne crée rien en silence ;
//  3. sans id, le nom cherche parmi les objets qu'aucune entrée n'a déjà
//     réclamés par leur id ; plusieurs candidats exigent l'id.
//
// uniqueNames dit que le nom est unique en base : deux entrées du même nom
// sont alors toujours refusées. Sinon (sondes, tâches, dont le nom peut se
// répéter en base), seules deux entrées sans id du même nom le sont : l'une
// d'elles désignerait l'objet de l'autre.
func (b *builder) match(entries []entryRef, objects []objectRef, uniqueNames bool) matching {
	result := matching{existing: make([]int, len(entries)), claimed: make([]bool, len(objects))}
	byID := make(map[string]int, len(objects))
	for index, object := range objects {
		byID[object.id] = index
	}
	firstIDLine := map[string]int{}
	for index, entry := range entries {
		result.existing[index] = createEntry
		if entry.id == "" {
			continue
		}
		if first, seen := firstIDLine[entry.id]; seen {
			b.refuse(KeyIDDuplicate, entry.line, entry.name, strconv.Itoa(first))
			result.existing[index] = refusedEntry
			continue
		}
		firstIDLine[entry.id] = entry.line
		found, known := byID[entry.id]
		if !known {
			b.refuse(KeyIDUnknown, entry.line, entry.name, entry.id)
			result.existing[index] = refusedEntry
			continue
		}
		result.existing[index] = found
		result.claimed[found] = true
	}
	b.refuseNameDuplicates(entries, result, uniqueNames)
	b.matchByName(entries, objects, result)
	return result
}

// refuseNameDuplicates refuse la seconde entrée d'un même nom.
func (b *builder) refuseNameDuplicates(entries []entryRef, result matching, uniqueNames bool) {
	// Le nom, puis s'il a déjà une entrée sans id.
	seen := map[string]bool{}
	seenWithoutID := map[string]bool{}
	for index, entry := range entries {
		if result.existing[index] == refusedEntry {
			continue
		}
		repeated := seen[entry.name] && (uniqueNames || (entry.id == "" && seenWithoutID[entry.name]))
		seen[entry.name] = true
		if entry.id == "" {
			seenWithoutID[entry.name] = true
		}
		if repeated {
			b.refuse(KeyNameDuplicate, entry.line, entry.name, entry.name)
			result.existing[index] = refusedEntry
		}
	}
}

// matchByName donne leur objet aux entrées sans id.
func (b *builder) matchByName(entries []entryRef, objects []objectRef, result matching) {
	for index, entry := range entries {
		if entry.id != "" || result.existing[index] == refusedEntry {
			continue
		}
		candidates := []int{}
		for found, object := range objects {
			if !result.claimed[found] && object.name == entry.name {
				candidates = append(candidates, found)
			}
		}
		switch len(candidates) {
		case 0:
			result.existing[index] = createEntry
		case 1:
			result.existing[index] = candidates[0]
			result.claimed[candidates[0]] = true
		default:
			b.refuse(KeyNameAmbiguous, entry.line, entry.name, entry.name)
			result.existing[index] = refusedEntry
		}
	}
}
