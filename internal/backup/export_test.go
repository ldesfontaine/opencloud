package backup

import "time"

// Due ouvre la règle du rythme aux tests de backup_test, qui ne peuvent pas
// être internes (le banc de test importe le store, qui importe ce paquet).
func Due(now time.Time, plan Plan) bool {
	return due(now, plan)
}
