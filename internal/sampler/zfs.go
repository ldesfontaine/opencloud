package sampler

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// Les deux lignes d'arcstats qui servent : la taille de l'ARC et son
// plancher, en octets.
type arcCounters struct {
	size    int64
	minimum int64
}

// readArcstats lit /proc/spl/kstat/zfs/arcstats (« nom type valeur » par
// ligne). Sans ZFS, le fichier n'existe pas : ok est faux, sans erreur. Un
// fichier illisible ou sans ligne size compte comme absent : un cache qu'on
// ne sait pas lire ne doit pas faire perdre la mesure du reste de la machine.
func readArcstats(path string) (arcCounters, bool) {
	content, err := readFile(path)
	if err != nil {
		return arcCounters{}, false
	}
	var counters arcCounters
	var hasSize bool
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			continue
		}
		value, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || value < 0 {
			continue
		}
		switch fields[0] {
		case "size":
			counters.size = value
			hasSize = true
		case "c_min":
			counters.minimum = value
		}
	}
	return counters, hasSize
}

// reclaimable est la part de l'ARC que ZFS rend quand un programme demande
// de la mémoire : ce qui dépasse son plancher.
func (c arcCounters) reclaimable() int64 {
	return max(c.size-c.minimum, 0)
}
