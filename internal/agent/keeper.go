package agent

import (
	"reflect"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/hostconn"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/role"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

// latestReport garde le dernier rapport d'une lecture de la machine et dit
// s'il reste à livrer : il part quand il a changé, et une fois à chaque
// ouverture du flux (le serveur garde un reflet, un flux neuf le lui redonne).
// C'est le patron de chaque lecture qui vit à part du flux ; le type du
// rapport est le seul qui change.
type latestReport[T any] struct {
	mu     sync.Mutex
	latest *T
	// isDelivered : le dernier signal a emporté le rapport courant.
	isDelivered bool
}

// Les lectures qui suivent ce patron.
type (
	roleReports       = latestReport[role.Report]
	hostReports       = latestReport[hostinfo.Report]
	unitReports       = latestReport[systemdunits.Report]
	connectionReports = latestReport[hostconn.Report]
)

// update garde le rapport et dit s'il diffère du précédent.
func (l *latestReport[T]) update(report T) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed := l.latest == nil || !reflect.DeepEqual(*l.latest, report)
	if changed {
		l.isDelivered = false
	}
	l.latest = &report
	return changed
}

// take rend le rapport s'il n'est pas encore parti, nil sinon.
func (l *latestReport[T]) take() *T {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.latest == nil || l.isDelivered {
		return nil
	}
	l.isDelivered = true
	return l.latest
}

// restore remet à livrer ce qu'un signal n'a pas pu emporter.
func (l *latestReport[T]) restore(report *T) {
	if report == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.isDelivered = false
}

// markReplayed : un flux neuf, ou un rappel, redit le rapport.
func (l *latestReport[T]) markReplayed() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.isDelivered = false
}
