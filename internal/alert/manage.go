package alert

import (
	"context"
	"strings"
)

// --- Canaux ---

func (e *Engine) CreateChannel(ctx context.Context, definition ChannelDefinition) (Channel, error) {
	if err := definition.Validate(); err != nil {
		return Channel{}, err
	}
	existing, err := e.store.ListChannels(ctx)
	if err != nil {
		return Channel{}, err
	}
	if len(existing) >= MaxChannels {
		return Channel{}, ErrTooManyChannels
	}
	channel := Channel{
		Name: strings.TrimSpace(definition.Name), URL: definition.URL, Format: definition.Format, Secret: definition.Secret,
		MinSeverity: definition.MinSeverity, NotifyResolve: definition.NotifyResolve, Enabled: definition.Enabled, CreatedAt: e.now(),
	}
	id, err := e.store.InsertChannel(ctx, channel)
	if err != nil {
		return Channel{}, err
	}
	e.logger.Info("alert channel created", "channel_id", id, "name", channel.Name, "format", string(channel.Format))
	e.changed()
	return e.store.GetChannel(ctx, id)
}

// UpdateChannel réécrit le canal ; un secret vide garde celui en place,
// ClearSecret le retire.
func (e *Engine) UpdateChannel(ctx context.Context, id int64, definition ChannelDefinition) (Channel, error) {
	if err := definition.Validate(); err != nil {
		return Channel{}, err
	}
	channel, err := e.store.GetChannel(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	channel.Name = strings.TrimSpace(definition.Name)
	channel.URL = definition.URL
	channel.Format = definition.Format
	channel.MinSeverity = definition.MinSeverity
	channel.NotifyResolve = definition.NotifyResolve
	channel.Enabled = definition.Enabled
	if definition.ClearSecret {
		channel.Secret = ""
	}
	if definition.Secret != "" {
		channel.Secret = definition.Secret
	}
	if err := e.store.UpdateChannel(ctx, channel); err != nil {
		return Channel{}, err
	}
	e.logger.Info("alert channel updated", "channel_id", id, "name", channel.Name, "enabled", channel.Enabled)
	e.changed()
	return e.store.GetChannel(ctx, id)
}

func (e *Engine) DeleteChannel(ctx context.Context, id int64) error {
	if err := e.store.DeleteChannel(ctx, id); err != nil {
		return err
	}
	e.logger.Info("alert channel deleted", "channel_id", id)
	e.changed()
	return nil
}

func (e *Engine) Channels(ctx context.Context) ([]Channel, error) {
	return e.store.ListChannels(ctx)
}

func (e *Engine) GetChannel(ctx context.Context, id int64) (Channel, error) {
	return e.store.GetChannel(ctx, id)
}

// --- Silences ---

func (e *Engine) CreateSilence(ctx context.Context, definition SilenceDefinition) (Silence, error) {
	if err := definition.Validate(); err != nil {
		return Silence{}, err
	}
	now := e.now()
	silence := Silence{
		Kind: definition.Kind, Object: definition.Object, Reason: strings.TrimSpace(definition.Reason),
		StartsAt: now, EndsAt: now.Add(definition.Duration), CreatedAt: now,
	}
	id, err := e.store.InsertSilence(ctx, silence)
	if err != nil {
		return Silence{}, err
	}
	silence.ID = id
	e.logger.Info("alert silence created", "silence_id", id, "kind", string(silence.Kind), "object_kind", string(silence.Object.Kind), "object_id", silence.Object.ID, "until", silence.EndsAt)
	e.changed()
	return silence, nil
}

func (e *Engine) DeleteSilence(ctx context.Context, id int64) error {
	if err := e.store.DeleteSilence(ctx, id); err != nil {
		return err
	}
	e.logger.Info("alert silence deleted", "silence_id", id)
	e.changed()
	return nil
}

// Silences rend les silences, les actifs d'abord.
func (e *Engine) Silences(ctx context.Context) ([]Silence, error) {
	return e.store.ListSilences(ctx)
}
