package resource

import "github.com/ldesfontaine/opencloud/internal/sampler"

// Ce que l'agent envoie avec son signal : les lectures faites depuis le
// signal précédent, au plus MaxReadingsPerSignal. Un signal sans corps
// reste un signal.
type SignalRequest struct {
	Readings []sampler.Reading `json:"readings"`
}
