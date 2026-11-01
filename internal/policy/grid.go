package policy

// Cell est une case de la grille : ce qui passe, et ce qui est refusé en
// toutes lettres, de la ligne vers la colonne.
type Cell struct {
	From    string
	To      string
	Allowed []Rule
	Denied  []Rule
}

// Grid est la grille « qui parle à qui » : une ligne par zone de départ, une
// colonne par zone d'arrivée. Une case absente de Cells est une case vide : ce
// qu'aucune règle n'autorise est refusé.
type Grid struct {
	Rows    []string
	Columns []string
	Cells   []Cell
	// Unplaced : les règles actives dont une zone n'est pas connue.
	Unplaced []Rule
	// Disabled : les règles éteintes, qui ne comptent pas.
	Disabled int
}

// Grid construit la grille des règles actives. Elle ne rejoue pas l'ordre
// d'évaluation du pare-feu : une règle de refus placée avant une règle
// passante la masque, la grille montre alors les deux.
func (m Model) Grid() Grid {
	grid := Grid{Rows: m.rows(), Columns: m.columns()}
	cells := map[[2]string]*Cell{}
	var order [][2]string
	put := func(rule Rule, from, to string) {
		key := [2]string{from, to}
		cell, found := cells[key]
		if !found {
			cell = &Cell{From: from, To: to}
			cells[key] = cell
			order = append(order, key)
		}
		if rule.Effect == EffectAllow {
			cell.Allowed = append(cell.Allowed, rule)
		} else {
			cell.Denied = append(cell.Denied, rule)
		}
	}
	for _, rule := range m.Rules {
		if !rule.Enabled {
			grid.Disabled++
			continue
		}
		destinations, placed := m.destinationsOf(rule)
		if !placed {
			grid.Unplaced = append(grid.Unplaced, rule)
			continue
		}
		for _, to := range destinations {
			put(rule, rule.From.Zone, to)
		}
	}
	for _, key := range order {
		grid.Cells = append(grid.Cells, *cells[key])
	}
	return grid
}

// rows : toutes les zones sauf le pare-feu, qui ne « part » nulle part.
func (m Model) rows() []string {
	var rows []string
	for _, zone := range m.Zones {
		if zone.Kind != ZoneSelf {
			rows = append(rows, zone.ID)
		}
	}
	return rows
}

func (m Model) columns() []string {
	columns := make([]string, 0, len(m.Zones))
	for _, zone := range m.Zones {
		columns = append(columns, zone.ID)
	}
	return columns
}

// destinationsOf rend les zones d'arrivée d'une règle ; faux si elle ne peut
// pas être placée. « Vers n'importe où » donne toutes les zones sauf celle de
// départ : le trafic d'une zone vers elle-même ne passe pas par le pare-feu.
func (m Model) destinationsOf(rule Rule) ([]string, bool) {
	if rule.From.Zone == UnknownZone || rule.From.Zone == AllZones || !m.hasRow(rule.From.Zone) {
		return nil, false
	}
	switch rule.To.Zone {
	case UnknownZone:
		return nil, false
	case AllZones:
		var zones []string
		for _, column := range m.columns() {
			if column != rule.From.Zone && column != rule.To.Except {
				zones = append(zones, column)
			}
		}
		return zones, true
	}
	return []string{rule.To.Zone}, true
}

func (m Model) hasRow(zone string) bool {
	for _, row := range m.rows() {
		if row == zone {
			return true
		}
	}
	return false
}
