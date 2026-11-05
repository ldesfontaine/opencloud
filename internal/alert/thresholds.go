package alert

import (
	"context"
	"errors"
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Rules rend la règle en vigueur de chaque ressource, et d'où elle vient.
// machineID vide : celles du réglage global, tel que la page Paramètres le
// montre.
func (e *Engine) Rules(ctx context.Context, machineID string) ([]Applied, error) {
	rows, err := e.store.ListThresholds(ctx, machineID)
	if err != nil {
		return nil, err
	}
	return resolve(rows, machineID), nil
}

// rulesOrDefaults : une lecture en échec ne coupe jamais l'alerte, elle
// juge avec les valeurs du produit.
func (e *Engine) rulesOrDefaults(ctx context.Context, machineID string) []Applied {
	rules, err := e.Rules(ctx, machineID)
	if err != nil {
		e.fail(ctx, "read thresholds", err)
		return resolve(nil, machineID)
	}
	return rules
}

// SetRule règle une ressource : pour toutes les machines sans surcharge
// quand machineID est vide, sinon pour cette machine seule.
func (e *Engine) SetRule(ctx context.Context, machineID string, rule Rule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	name, err := e.thresholdScopeName(ctx, machineID)
	if err != nil {
		return err
	}
	if err := e.store.UpsertThreshold(ctx, Threshold{MachineID: machineID, Rule: rule}); err != nil {
		return err
	}
	e.auditor.Record(ctx, thresholdEvent(machineID, name, rule))
	return nil
}

// ClearRule retire la surcharge d'une machine : elle suit de nouveau le
// réglage global. Rien à retirer n'est pas une erreur.
func (e *Engine) ClearRule(ctx context.Context, machineID string, resource Resource) error {
	if machineID == "" || !isResource(resource) {
		return ErrThresholdInvalid
	}
	name, err := e.thresholdScopeName(ctx, machineID)
	if err != nil {
		return err
	}
	if err := e.store.DeleteThreshold(ctx, machineID, resource); err != nil {
		return err
	}
	e.auditor.Record(ctx, thresholdClearedEvent(machineID, name, resource))
	return nil
}

// thresholdScopeName : le nom de la machine visée, ErrNotFound si elle n'existe
// pas ; vide pour le réglage global.
func (e *Engine) thresholdScopeName(ctx context.Context, machineID string) (string, error) {
	if machineID == "" {
		return "", nil
	}
	name, err := e.store.MuteTargetName(ctx, Target{Kind: TargetMachine, ID: machineID})
	if errors.Is(err, ErrNotFound) {
		return "", ErrNotFound
	}
	return name, err
}

const (
	auditKeyThresholds = "thresholds"
)

func thresholdEvent(machineID, machineName string, rule Rule) audit.Event {
	event := audit.Event{
		Action: audit.SettingsUpdate, ObjectID: auditKeyThresholds, ObjectName: auditKeyThresholds,
		Details: audit.Details{
			"resource":          string(rule.Resource),
			"attention_percent": strconv.Itoa(rule.Attention),
			"danger_percent":    strconv.Itoa(rule.Danger),
			"window_minutes":    strconv.Itoa(int(rule.Window.Minutes())),
		},
	}
	if machineID != "" {
		event.Details["machine_id"] = machineID
		event.Details["machine_name"] = machineName
	}
	return event
}

func thresholdClearedEvent(machineID, machineName string, resource Resource) audit.Event {
	return audit.Event{
		Action: audit.SettingsUpdate, ObjectID: auditKeyThresholds, ObjectName: auditKeyThresholds,
		Details: audit.Details{
			"resource": string(resource), "machine_id": machineID, "machine_name": machineName, "cleared": "true",
		},
	}
}
