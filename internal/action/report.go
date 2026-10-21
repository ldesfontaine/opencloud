package action

import (
	"context"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

// Record écrit ce que l'agent d'une machine rapporte de ses ordres, par la
// section actions de son signal. Un rapport hors des bornes du protocole
// est refusé en entier (ErrReportInvalid) ; un run inconnu, d'une autre
// machine ou déjà fini est ignoré : l'agent rejoue ce qu'une coupure a
// retenu, et une réponse arrivée après l'expiration ne la défait pas.
func (s *Service) Record(ctx context.Context, machineID string, report agentwire.ActionReport) error {
	results, err := readReport(report)
	if err != nil {
		return err
	}
	now := s.now()
	isChanged := false
	for _, result := range results {
		run, changed, err := s.store.ApplyRunResult(ctx, machineID, result, now)
		if err != nil {
			return fmt.Errorf("apply run result: %w", err)
		}
		if !changed {
			s.logger.Debug("run result ignored", "machine_id", machineID, "run_id", result.RunID, "state", string(result.State))
			continue
		}
		isChanged = true
		s.logger.Info("run result", "machine_id", machineID, "run_id", result.RunID, "state", string(result.State), "reason", result.Reason)
		if run.State == StateRefused {
			s.auditor.Record(ctx, refusedEvent(run))
		}
		// L'agent a pris la clé courante : c'est elle qui signera ses ordres.
		if run.Kind == agentwire.KindServerKey && run.State == StateSucceeded && s.key != nil {
			if err := s.machines.NoteServerKey(ctx, run.AgentID, s.key.Fingerprint()); err != nil {
				return fmt.Errorf("note the new server key: %w", err)
			}
		}
	}
	if isChanged {
		s.changed(machineID)
	}
	return nil
}

// readReport juge un rapport contre les bornes du protocole et le rend dans
// les types du composant.
func readReport(report agentwire.ActionReport) ([]Result, error) {
	if len(report.Results) > agentwire.MaxActionResults {
		return nil, fmt.Errorf("%w: %d results", ErrReportInvalid, len(report.Results))
	}
	results := make([]Result, 0, len(report.Results))
	for _, reported := range report.Results {
		result, err := readResult(reported)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func readResult(reported agentwire.ActionResult) (Result, error) {
	if reported.RunID == "" || len(reported.RunID) > maxRunIDBytes {
		return Result{}, fmt.Errorf("%w: run id", ErrReportInvalid)
	}
	state, ok := agentState(reported.State)
	if !ok {
		return Result{}, fmt.Errorf("%w: state %q", ErrReportInvalid, reported.State)
	}
	if !isReason(reported.Reason) {
		return Result{}, fmt.Errorf("%w: reason", ErrReportInvalid)
	}
	if len(reported.Output) > agentwire.MaxOutputLines {
		return Result{}, fmt.Errorf("%w: %d output lines", ErrReportInvalid, len(reported.Output))
	}
	output := make([]LogLine, 0, len(reported.Output))
	for _, line := range reported.Output {
		if (line.Stream != "stdout" && line.Stream != "stderr") || len(line.Line) > agentwire.MaxOutputLineBytes {
			return Result{}, fmt.Errorf("%w: output line", ErrReportInvalid)
		}
		output = append(output, LogLine{Stream: line.Stream, Line: line.Line})
	}
	return Result{RunID: reported.RunID, State: state, Reason: reported.Reason, Output: output}, nil
}

// agentState : seuls ces quatre états viennent d'un agent.
func agentState(reported agentwire.ActionState) (State, bool) {
	switch reported {
	case agentwire.ActionAccepted:
		return StateAccepted, true
	case agentwire.ActionRefused:
		return StateRefused, true
	case agentwire.ActionSucceeded:
		return StateSucceeded, true
	case agentwire.ActionFailed:
		return StateFailed, true
	}
	return "", false
}

// isReason : du texte ASCII imprimable, court, sur une ligne. La liste des
// raisons vit chez l'agent ; le serveur garde ce qu'il reçoit tant que la
// forme tient, l'interface traduit ce qu'elle connaît.
func isReason(reason string) bool {
	if len(reason) > maxReasonBytes {
		return false
	}
	for _, character := range reason {
		if character < ' ' || character > '~' {
			return false
		}
	}
	return true
}
