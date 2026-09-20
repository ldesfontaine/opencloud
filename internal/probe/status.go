package probe

import (
	"strconv"
	"strings"
)

// Le code attendu d'une sonde HTTP s'écrit par familles ou par codes :
// « 2xx », « 200 », « 200,301 ». Vide vaut « 2xx ».

type statusRange struct {
	low  int
	high int
}

func parseStatusPattern(pattern string) ([]statusRange, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return []statusRange{{low: 200, high: 299}}, true
	}
	var ranges []statusRange
	for _, part := range strings.Split(pattern, ",") {
		parsed, ok := parseStatusPart(strings.TrimSpace(strings.ToLower(part)))
		if !ok {
			return nil, false
		}
		ranges = append(ranges, parsed)
	}
	if len(ranges) == 0 {
		return nil, false
	}
	return ranges, true
}

func parseStatusPart(part string) (statusRange, bool) {
	if len(part) == 3 && part[1] == 'x' && part[2] == 'x' {
		family := int(part[0] - '0')
		if family >= 1 && family <= 5 {
			return statusRange{low: family * 100, high: family*100 + 99}, true
		}
		return statusRange{}, false
	}
	code, err := strconv.Atoi(part)
	if err != nil || code < 100 || code > 599 {
		return statusRange{}, false
	}
	return statusRange{low: code, high: code}, true
}

func isStatusPattern(pattern string) bool {
	_, ok := parseStatusPattern(pattern)
	return ok
}

// matchStatus dit si le code reçu est celui qu'on attendait. Un motif
// illisible n'est jamais accepté à la création : ici, il refuse tout.
func matchStatus(pattern string, code int) bool {
	ranges, ok := parseStatusPattern(pattern)
	if !ok {
		return false
	}
	for _, candidate := range ranges {
		if code >= candidate.low && code <= candidate.high {
			return true
		}
	}
	return false
}
