package dockerapi

import (
	"context"
	"net/url"
	"time"
)

// Stats est une mesure d'un conteneur en un coup : les compteurs bruts,
// dont le processeur cumulé qu'il faut comparer à la mesure précédente.
type Stats struct {
	Read   string
	CPU    CPUStats    `json:"cpu_stats"`
	Memory MemoryStats `json:"memory_stats"`
}

type CPUStats struct {
	Usage struct {
		Total uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	System     uint64 `json:"system_cpu_usage"`
	OnlineCPUs int    `json:"online_cpus"`
}

type MemoryStats struct {
	Usage uint64
	Limit uint64
	// inactive_file est du cache que le noyau peut rendre : on ne le compte
	// pas comme utilisé, comme docker stats.
	Stats struct {
		InactiveFile uint64 `json:"inactive_file"`
	}
}

// ReadAt rend l'instant de la mesure.
func (s Stats) ReadAt() time.Time {
	return ParseTime(s.Read)
}

// MemoryUsed rend la mémoire vraiment occupée, cache de fichiers déduit.
func (s Stats) MemoryUsed() uint64 {
	if s.Memory.Stats.InactiveFile > s.Memory.Usage {
		return 0
	}
	return s.Memory.Usage - s.Memory.Stats.InactiveFile
}

// Stats lit une mesure en un coup : le démon répond tout de suite, sans
// attendre une seconde pour faire son propre delta ; le delta est à nous.
func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	var stats Stats
	path := "/containers/" + url.PathEscape(id) + "/stats?stream=false&one-shot=true"
	if err := c.getJSON(ctx, path, &stats); err != nil {
		return Stats{}, err
	}
	return stats, nil
}
