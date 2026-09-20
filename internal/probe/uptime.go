package probe

import "time"

// Source dit quelle table une fenêtre d'uptime lit.
type Source string

const (
	SourceRaw   Source = "raw"
	SourceDaily Source = "daily"
)

// Window est une fenêtre d'uptime. Chaque fenêtre lit une table gardée
// strictement plus longtemps qu'elle : le brut sept jours pour la fenêtre
// de 24 h, l'agrégat journalier un an pour les trois autres.
type Window struct {
	Name   string
	Span   time.Duration
	Source Source
}

var Windows = []Window{
	{Name: "24h", Span: 24 * time.Hour, Source: SourceRaw},
	{Name: "7d", Span: 7 * 24 * time.Hour, Source: SourceDaily},
	{Name: "30d", Span: 30 * 24 * time.Hour, Source: SourceDaily},
	{Name: "90d", Span: 90 * 24 * time.Hour, Source: SourceDaily},
}

// Uptime est ce que le serveur rend d'une fenêtre : des essais et des
// succès. Le pourcentage se fait dans le navigateur, et une fenêtre sans
// essai n'en a pas, plutôt qu'un zéro qui ferait croire à une panne.
type Uptime struct {
	Window  string
	Total   int
	Success int
}
