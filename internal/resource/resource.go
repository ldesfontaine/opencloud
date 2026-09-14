package resource

import (
	"errors"
	"math"
	"time"

	"github.com/ldesfontaine/opencloud/internal/sampler"
)

const (
	// L'agent mesure toutes les 10 s et envoie par lot avec son signal.
	SampleInterval = 10 * time.Second
	// Passé ce délai sans échantillon, la machine est « indisponible » :
	// trois signaux manqués, jamais « à 0 % ».
	StaleAfter = 90 * time.Second
	// Ce qu'un signal peut porter : un lot de rattrapage borné.
	MaxReadingsPerSignal = 120
	// Un échantillon daté d'après cette avance est refusé : horloge fausse.
	maxFutureSkew = 5 * time.Minute

	// Chaque fenêtre lit une table gardée strictement plus longtemps qu'elle.
	RawRetention    = 48 * time.Hour
	HourlyRetention = 90 * 24 * time.Hour
	DailyRetention  = 365 * 24 * time.Hour
)

// Sample est un échantillon en base : une lecture et sa machine.
type Sample struct {
	MachineID string
	sampler.Reading
}

// Current est la valeur courante d'une machine : le dernier échantillon,
// et s'il est assez frais pour être montré comme tel.
type Current struct {
	MachineID string
	Available bool
	Sample    *Sample
}

// Source dit quelle table une fenêtre lit.
type Source string

const (
	SourceRaw    Source = "raw"
	SourceHourly Source = "hourly"
	SourceDaily  Source = "daily"
)

// Window est une fenêtre d'historique : sa durée, le pas des points, la
// table qui la sert.
type Window struct {
	Name   string
	Span   time.Duration
	Step   time.Duration
	Source Source
}

// Les fenêtres, toutes ouvertes ; le nom est ce que l'API reçoit.
var Windows = []Window{
	{Name: "1h", Span: time.Hour, Step: SampleInterval, Source: SourceRaw},
	{Name: "24h", Span: 24 * time.Hour, Step: 5 * time.Minute, Source: SourceRaw},
	{Name: "7d", Span: 7 * 24 * time.Hour, Step: time.Hour, Source: SourceHourly},
	{Name: "30d", Span: 30 * 24 * time.Hour, Step: time.Hour, Source: SourceHourly},
	{Name: "90d", Span: 90 * 24 * time.Hour, Step: 24 * time.Hour, Source: SourceDaily},
}

var (
	ErrNoSample        = errors.New("no sample for this machine")
	ErrWindowUnknown   = errors.New("unknown history window")
	ErrReadingInvalid  = errors.New("reading out of range")
	ErrTooManyReadings = errors.New("too many readings in one signal")
)

func WindowByName(name string) (Window, bool) {
	for _, window := range Windows {
		if window.Name == name {
			return window, true
		}
	}
	return Window{}, false
}

// validate refuse ce qu'un agent ne peut pas avoir mesuré : un pourcentage
// hors de 0 à 100, un nombre négatif, un NaN, une date absurde.
func validate(reading sampler.Reading, now time.Time) error {
	if reading.SampledAt.IsZero() || reading.SampledAt.After(now.Add(maxFutureSkew)) || reading.SampledAt.Before(now.Add(-RawRetention)) {
		return ErrReadingInvalid
	}
	if !isFinite(reading.CPUPercent) || reading.CPUPercent < 0 || reading.CPUPercent > 100 || !isFinite(reading.Load1) || reading.Load1 < 0 {
		return ErrReadingInvalid
	}
	for _, value := range []int64{
		int64(reading.CPUCores), reading.MemUsed, reading.MemTotal, reading.SwapUsed, reading.SwapTotal,
		reading.DiskUsed, reading.DiskTotal, reading.NetRxPerSecond, reading.NetTxPerSecond,
	} {
		if value < 0 {
			return ErrReadingInvalid
		}
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
