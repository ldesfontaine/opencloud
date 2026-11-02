package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Le producteur « export de la configuration d'un pare-feu » : c'est le
// serveur qui le joue, pas un agent. La clé d'API du pare-feu ne quitte donc
// pas la machine openCloud, et le fichier lu n'en sort que chiffré. Le plan
// qui l'utilise a pour machine source la machine openCloud et pour sélecteur
// l'identifiant du pare-feu déclaré. Il n'a aucun droit d'agent : rien ne
// s'exécute sur une autre machine que la destination.
const (
	ProducerOPNsense = "opnsense-config"

	// KindFirewall est le genre de source d'un pare-feu déclaré.
	KindFirewall = "firewall"

	// configurationEntry est le nom du fichier dans l'archive : celui que
	// OPNsense attend à la restauration (Système → Configuration → Sauvegardes).
	configurationEntry = "config.xml"
)

var firewallSelectorPattern = regexp.MustCompile(`^[a-z0-9]{1,64}$`)

var (
	ErrFirewallUnknown    = refusal.Invalid("backup.firewall_unknown", "the firewall is not declared in the Firewalls module")
	ErrFirewallNotWired   = errors.New("backup: no firewall module wired")
	ErrFirewallUnreadable = refusal.Conflict("backup.firewall_unreadable", "the firewall configuration could not be read")
)

// Firewalls est ce que la sauvegarde attend du module Pare-feu : les pare-feu
// déclarés et la lecture de la configuration de l'un d'eux.
type Firewalls interface {
	Declared(ctx context.Context) ([]firewall.Summary, error)
	ExportConfiguration(ctx context.Context, id string) ([]byte, error)
}

func (s *Service) SetFirewalls(firewalls Firewalls) { s.firewalls = firewalls }

// FirewallSourceKey nomme la configuration d'un pare-feu dans l'historique.
func FirewallSourceKey(firewallID string) string { return "firewall:" + firewallID }

// IsFirewallExport dit si le serveur produit lui-même la sauvegarde du plan.
func (p Plan) IsFirewallExport() bool {
	return p.SourceKind == SourceAgent && p.Producer == ProducerOPNsense
}

// sourceKey est la clé d'historique d'un plan que le serveur produit lui-même.
func (p Plan) sourceKey() string {
	if p.IsFirewallExport() {
		return FirewallSourceKey(p.Selector)
	}
	return SourceDatabaseKey
}

// producedByServer : la base ou un pare-feu, par opposition à un agent.
func (p Plan) producedByServer() bool {
	return p.SourceKind == SourceDatabase || p.IsFirewallExport()
}

func (d PlanDefinition) validateFirewallExport() error {
	if d.SourceMachineID != machine.LocalID {
		return ErrSourceMachineUnknown
	}
	if d.Selector == SelectorAll || !firewallSelectorPattern.MatchString(d.Selector) {
		return ErrSelectorInvalid
	}
	return nil
}

// checkNewFirewallPlan ne juge que la création : changer le nombre gardé d'un
// plan existant ne demande pas que le pare-feu réponde.
func (s *Service) checkNewFirewallPlan(ctx context.Context, definition PlanDefinition) error {
	if definition.Producer != ProducerOPNsense {
		return nil
	}
	_, err := s.store.GetBackupPlan(ctx, definition.Subject)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("read backup plan: %w", err)
	}
	return s.checkFirewallExport(ctx, definition.Selector)
}

// checkFirewallExport juge, à la création d'un plan, que le pare-feu est
// déclaré et que sa clé a le droit d'exporter : un refus ici dit le libellé du
// droit à ajouter, au lieu d'attendre la nuit.
func (s *Service) checkFirewallExport(ctx context.Context, firewallID string) error {
	if s.firewalls == nil {
		return ErrFirewallNotWired
	}
	declared, err := s.firewalls.Declared(ctx)
	if err != nil {
		return fmt.Errorf("list declared firewalls: %w", err)
	}
	if !containsFirewall(declared, firewallID) {
		return ErrFirewallUnknown
	}
	return s.readFirewall(ctx, firewallID, nil)
}

func containsFirewall(declared []firewall.Summary, id string) bool {
	for _, candidate := range declared {
		if candidate.ID == id {
			return true
		}
	}
	return false
}

// readFirewall lit la configuration et la rend à use, qui ne la garde pas. Une
// panne du pare-feu devient un refus fermé ; ErrExportForbidden passe tel quel.
func (s *Service) readFirewall(ctx context.Context, firewallID string, use func(content []byte) error) error {
	content, err := s.firewalls.ExportConfiguration(ctx, firewallID)
	if errors.Is(err, firewall.ErrNotFound) {
		return ErrFirewallUnknown
	}
	if err != nil {
		if _, isRefusal := refusal.KeyOf(err); isRefusal {
			return err
		}
		return fmt.Errorf("%w: %w", ErrFirewallUnreadable, err)
	}
	if use == nil {
		return nil
	}
	return use(content)
}

// stageFirewall écrit dans le colis l'archive chiffrée de la configuration et
// la clé scellée. Le fichier lu n'existe qu'en mémoire : il n'est jamais écrit
// en clair sur le disque du serveur.
func (s *Service) stageFirewall(ctx context.Context, writer *action.ParcelWriter, keyring Keyring, plan Plan, at time.Time) error {
	recipient, err := age.ParseX25519Recipient(keyring.Recipient)
	if err != nil {
		return fmt.Errorf("parse backup recipient: %w", err)
	}
	return s.readFirewall(ctx, plan.Selector, func(content []byte) error {
		archive, err := writer.Create(agentwire.BackupFileName(plan.Subject, at))
		if err != nil {
			return err
		}
		packErr := WriteEntryArchive(archive, recipient, configurationEntry, bytes.NewReader(content), int64(len(content)))
		if err := errors.Join(packErr, archive.Close()); err != nil {
			return fmt.Errorf("pack backup: %w", err)
		}
		return s.stageSealedKey(writer, keyring)
	})
}
