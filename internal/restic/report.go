package restic

import (
	"context"
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// RecordReport écrit la section restic du signal d'une machine. Elle est
// rejugée ici contre ses bornes (agentwire.ErrResticReportInvalid : refusée
// en entier) : le serveur ne croit pas l'agent sur parole. Aucun mot de passe
// ni empreinte n'y passe : le serveur tire ceux des liens et les distribue
// par le secret des ordres. Ce que la cible ou la source a perdu ou changé
// leur repart (healTarget, healSource).
func (s *Service) RecordReport(ctx context.Context, machineID string, report agentwire.ResticReport) error {
	if err := report.Validate(); err != nil {
		return err
	}
	ruleCtx := ruleContext(ctx)
	if report.Target != nil {
		if err := s.recordTarget(ruleCtx, machineID, *report.Target); err != nil {
			return err
		}
	}
	if report.Source != nil {
		s.healSource(ruleCtx, machineID, *report.Source)
	}
	return nil
}

// recordTarget n'écrit que pour une cible déclarée : un agent qui a le droit
// sans que l'opérateur l'ait posé n'entre pas en base.
func (s *Service) recordTarget(ctx context.Context, machineID string, report agentwire.ResticTargetReport) error {
	previous, err := s.store.GetResticTarget(ctx, machineID)
	if errors.Is(err, ErrTargetNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get restic target: %w", err)
	}
	recorded, err := s.store.RecordResticTargetReport(ctx, machineID, report, s.now())
	if err != nil {
		return fmt.Errorf("record restic target report: %w", err)
	}
	if !recorded {
		return nil
	}
	current := previous
	current.Reported = report
	s.healTarget(ctx, previous, current)
	s.changed()
	return nil
}
