package alert

import "time"

// Event est ce qu'une livraison annonce d'une alerte.
type Event string

const (
	EventOpened     Event = "opened"
	EventAggravated Event = "aggravated"
	EventResolved   Event = "resolved"
	EventTest       Event = "test"
)

func Events() []Event {
	return []Event{EventOpened, EventAggravated, EventResolved, EventTest}
}

type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryFailed    DeliveryStatus = "failed"
)

func DeliveryStatuses() []DeliveryStatus {
	return []DeliveryStatus{DeliveryPending, DeliveryDelivered, DeliveryFailed}
}

// Delivery est une livraison réservée en base avant d'être envoyée : si
// le processus meurt entre les deux, la ligne restée en attente repart au
// démarrage, et l'index unique (alerte, canal, événement) empêche d'envoyer
// deux fois.
type Delivery struct {
	ID        int64
	AlertID   int64
	ChannelID int64
	Event     Event
	Status    DeliveryStatus
	Attempts  int
	// Reason dit pourquoi le dernier essai a échoué, par un mot d'une liste
	// fermée que le front traduit ; Code est le code HTTP reçu, s'il y en
	// a eu un.
	Reason    Reason
	Code      int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Reason est le motif d'un échec de livraison, par un mot d'une liste
// fermée : le front le traduit, il ne lit jamais le texte d'une erreur Go.
type Reason string

const (
	ReasonNone        Reason = ""
	ReasonTimeout     Reason = "timeout"
	ReasonRefused     Reason = "refused"
	ReasonDNS         Reason = "dns"
	ReasonUnreachable Reason = "unreachable"
	ReasonForbidden   Reason = "forbidden"
	ReasonRedirect    Reason = "redirect"
	ReasonStatus      Reason = "status"
	ReasonTLS         Reason = "tls"
)

// Reasons liste les motifs d'échec ; le motif vide, celui d'une livraison
// réussie, n'en est pas un.
func Reasons() []Reason {
	return []Reason{ReasonTimeout, ReasonRefused, ReasonDNS, ReasonUnreachable, ReasonForbidden, ReasonRedirect, ReasonStatus, ReasonTLS}
}

const (
	// Trois essais : tout de suite, puis après ces attentes. Une panne du
	// récepteur plus longue perd la livraison, et la page le montre.
	MaxAttempts    = 3
	RequestTimeout = 10 * time.Second
	// Une livraison restée en attente plus longtemps que cela au démarrage
	// est rejouée une fois, puis abandonnée si elle échoue.
	PendingRetryAfter = 5 * time.Minute
)

// Backoffs sont les attentes avant le deuxième et le troisième essai.
var Backoffs = []time.Duration{5 * time.Second, 30 * time.Second}
