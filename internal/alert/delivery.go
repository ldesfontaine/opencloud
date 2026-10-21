package alert

import "time"

// Event est ce qu'une livraison annonce d'une alerte.
type Event string

const (
	EventOpened     Event = "opened"
	EventAggravated Event = "aggravated"
	EventResolved   Event = "resolved"
	EventTest       Event = "test"
	// EventSummary dit combien d'envois la limite de débit a retenus ; il
	// n'est la livraison d'aucune alerte.
	EventSummary Event = "summary"
)

func Events() []Event {
	return []Event{EventOpened, EventAggravated, EventResolved, EventTest, EventSummary}
}

type DeliveryStatus string

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryFailed    DeliveryStatus = "failed"
	// Retenue par la limite de débit du canal : jamais envoyée, comptée
	// dans le message groupé qui suit.
	DeliveryHeld DeliveryStatus = "held"
	// Tue par une fenêtre au moment de l'envoi : jamais envoyée.
	DeliveryMuted DeliveryStatus = "muted"
)

func DeliveryStatuses() []DeliveryStatus {
	return []DeliveryStatus{DeliveryPending, DeliveryDelivered, DeliveryFailed, DeliveryHeld, DeliveryMuted}
}

// Delivery est une livraison réservée en base avant d'être envoyée : si
// le processus meurt entre les deux, la ligne restée en attente repart au
// démarrage, et l'index unique (alerte, canal, événement) empêche d'envoyer
// deux fois. En échec, elle porte l'heure de son essai suivant, que le
// balayage rejoue ; zéro, elle est abandonnée.
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
	Reason        Reason
	Code          int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	NextAttemptAt time.Time
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
	// Les refus d'un relais SMTP ; Code porte alors le code SMTP reçu.
	ReasonMailNotConfigured Reason = "mail_not_configured"
	ReasonSMTPAuthFailed    Reason = "smtp_auth_failed"
	ReasonSMTPRefused       Reason = "smtp_refused"
	ReasonSMTPTLS           Reason = "smtp_tls"
	ReasonSMTPPlain         Reason = "smtp_plain"
)

// Reasons liste les motifs d'échec ; le motif vide, celui d'une livraison
// réussie, n'en est pas un.
func Reasons() []Reason {
	return []Reason{
		ReasonTimeout, ReasonRefused, ReasonDNS, ReasonUnreachable, ReasonForbidden, ReasonRedirect, ReasonStatus, ReasonTLS,
		ReasonMailNotConfigured, ReasonSMTPAuthFailed, ReasonSMTPRefused, ReasonSMTPTLS, ReasonSMTPPlain,
	}
}

const (
	// Les premiers essais se tiennent dans l'ouvrier : tout de suite, puis
	// après les deux premières attentes de Backoffs. Les suivants passent
	// par la base, un essai à chaque fois.
	ImmediateAttempts = 3
	// Passé ce délai depuis sa réservation, une livraison en échec est
	// abandonnée, et la fiche de l'alerte le montre.
	RetrySpan      = 24 * time.Hour
	RequestTimeout = 10 * time.Second
	// Un relais SMTP dialogue plus longtemps qu'un webhook : connexion,
	// STARTTLS, AUTH, enveloppe et corps tiennent dans ce délai.
	MailTimeout = 20 * time.Second
)

// Backoffs sont les attentes entre deux essais ; la dernière se répète
// jusqu'à RetrySpan. Le balayage de 30 s rejoue ce qui est échu : à partir
// de la troisième, un essai part au plus 30 s après son heure.
var Backoffs = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// nextAttempt rend l'heure de l'essai qui suit un échec, ou zéro quand
// elle passerait RetrySpan depuis la réservation.
func nextAttempt(delivery Delivery, backoffs []time.Duration, now time.Time) time.Time {
	wait := backoffs[min(max(delivery.Attempts-1, 0), len(backoffs)-1)]
	at := now.Add(wait)
	if at.After(delivery.CreatedAt.Add(RetrySpan)) {
		return time.Time{}
	}
	return at
}
