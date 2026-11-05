package settings

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Ce que le service attend de la base ; le paquet store le fournit.
type Store interface {
	// LoadSettings rend des réglages vides tant que rien n'est réglé.
	LoadSettings(ctx context.Context) (Settings, error)
	// UpdateSettings relit, applique change et réécrit dans une seule
	// transaction ; rend les réglages écrits.
	UpdateSettings(ctx context.Context, change func(*Settings)) (Settings, error)
}

// Le délai du rappel est hors de 0 à alert.MaxReminderHours heures.
var ErrReminderInvalid = refusal.Invalid("settings.reminder_invalid", "settings: danger reminder invalid")

// Le code est celui du commutateur de langue : la clé existe déjà dans les
// catalogues.
var ErrLanguageUnknown = refusal.Invalid("error.unknown_language", "settings: unknown language")

// Service lit et écrit les réglages de l'opérateur. Chaque geste ne touche
// que ses clés : les autres vivent sur la même ligne, et UpdateSettings les
// préserve. Rien n'est gardé en mémoire : un autre processus a pu changer
// la base.
type Service struct {
	store   Store
	auditor *audit.Recorder
	logger  *slog.Logger
}

func New(store Store, logger *slog.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// --- Les gestes ---

func (s *Service) SetLanguage(ctx context.Context, code lang.Code) error {
	if _, ok := lang.Parse(string(code)); !ok {
		return ErrLanguageUnknown
	}
	_, err := s.store.UpdateSettings(ctx, func(current *Settings) {
		current.Language = code
	})
	if err != nil {
		return err
	}
	s.auditor.Record(ctx, settingsEvent(keyLanguage, audit.Details{"language": string(code)}))
	return nil
}

// MailRelayChange est le relais saisi par l'opérateur. Un hôte vide retire
// le relais ; un mot de passe vide garde celui en place, sauf si
// ClearPassword le retire.
type MailRelayChange struct {
	Relay         mail.Relay
	ClearPassword bool
}

// SetMailRelay complète puis valide le relais saisi, et n'écrit que ses
// clés. Un port absent suit le mode TLS ; un relais sans hôte efface tout.
// Un mot de passe laissé vide garde celui de la base, relu dans la même
// transaction : il a été validé quand il a été écrit.
func (s *Service) SetMailRelay(ctx context.Context, change MailRelayChange) (mail.Relay, error) {
	relay := change.Relay
	relay.Host = strings.TrimSpace(relay.Host)
	relay.From = strings.TrimSpace(relay.From)
	if relay.Port == 0 {
		relay.Port = mail.DefaultPortFor(relay.TLS)
	}
	if relay.Host != "" {
		if err := relay.Validate(); err != nil {
			return mail.Relay{}, err
		}
	}
	keepPassword := relay.Password == "" && !change.ClearPassword
	written, err := s.store.UpdateSettings(ctx, func(current *Settings) {
		if keepPassword {
			relay.Password = current.MailPassword
		}
		current.SetMailRelay(relay)
	})
	if err != nil {
		return mail.Relay{}, err
	}
	s.auditor.Record(ctx, mailRelayEvent(written.MailRelay(), !keepPassword))
	return written.MailRelay(), nil
}

// SetDangerReminderHours règle au bout de combien d'heures une alerte de
// danger sans lecture est rappelée ; zéro coupe le rappel.
func (s *Service) SetDangerReminderHours(ctx context.Context, hours int) error {
	if hours < 0 || hours > alert.MaxReminderHours {
		return ErrReminderInvalid
	}
	_, err := s.store.UpdateSettings(ctx, func(current *Settings) {
		current.DangerReminderHours = hours
	})
	if err != nil {
		return err
	}
	s.auditor.Record(ctx, settingsEvent(keyReminder, audit.Details{"hours": strconv.Itoa(hours)}))
	return nil
}

// SetLifecycleEnabled dit si la fin de support vient d'être rallumée,
// d'après la valeur relue dans la même transaction : l'appelant presse
// alors une lecture de la source, sans attendre la cadence.
func (s *Service) SetLifecycleEnabled(ctx context.Context, enabled bool) (isTurnedOn bool, err error) {
	wasEnabled := false
	_, err = s.store.UpdateSettings(ctx, func(current *Settings) {
		wasEnabled = current.LifecycleEnabled()
		current.LifecycleDisabled = !enabled
	})
	if err != nil {
		return false, err
	}
	s.auditor.Record(ctx, settingsEvent(keyLifecycle, audit.Details{"enabled": strconv.FormatBool(enabled)}))
	return enabled && !wasEnabled, nil
}

// --- Les lectures ---

func (s *Service) Language(ctx context.Context) (lang.Code, error) {
	current, err := s.store.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	return current.InterfaceLanguage(), nil
}

func (s *Service) MailRelay(ctx context.Context) (mail.Relay, error) {
	current, err := s.store.LoadSettings(ctx)
	if err != nil {
		return mail.Relay{}, err
	}
	return current.MailRelay(), nil
}

// DangerReminderHours rend le délai du rappel, en heures ; zéro, coupé.
func (s *Service) DangerReminderHours(ctx context.Context) (int, error) {
	current, err := s.store.LoadSettings(ctx)
	if err != nil {
		return 0, err
	}
	return current.DangerReminderHours, nil
}

func (s *Service) LifecycleEnabled(ctx context.Context) (bool, error) {
	current, err := s.store.LoadSettings(ctx)
	if err != nil {
		return false, err
	}
	return current.LifecycleEnabled(), nil
}

// --- Les lectures au moment de servir ---

// Les composants qui demandent une simple fonction (le notifieur, le
// moteur des alertes, la fin de support) relisent la base à chaque appel :
// un réglage changé sous Paramètres vaut aussitôt. Ces lectures ne suivent
// pas l'arrêt du processus, pour qu'une alerte partie pendant l'arrêt garde
// sa langue et son relais ; une lecture en échec rend une valeur de repli.

// CurrentLanguage : en échec, la langue par défaut.
func (s *Service) CurrentLanguage() lang.Code {
	current, err := s.store.LoadSettings(context.Background())
	if err != nil {
		return lang.Default
	}
	return current.InterfaceLanguage()
}

// CurrentMailRelay : en échec, un relais vide, et l'envoi le dit par son
// motif.
func (s *Service) CurrentMailRelay() mail.Relay {
	current, err := s.store.LoadSettings(context.Background())
	if err != nil {
		return mail.Relay{}
	}
	return current.MailRelay()
}

// CurrentDangerReminder : en échec, le délai du produit, pour que le rappel
// ne se coupe pas sur une panne de lecture.
func (s *Service) CurrentDangerReminder() time.Duration {
	current, err := s.store.LoadSettings(context.Background())
	if err != nil {
		s.logger.Warn("read danger reminder", "error", err)
		return alert.DefaultReminderHours * time.Hour
	}
	return current.DangerReminder()
}

// CurrentLifecycleEnabled : en échec, coupée, puisqu'on ne sait pas si
// l'opérateur l'a coupée.
func (s *Service) CurrentLifecycleEnabled() bool {
	current, err := s.store.LoadSettings(context.Background())
	if err != nil {
		s.logger.Warn("read lifecycle setting", "error", err)
		return false
	}
	return current.LifecycleEnabled()
}
