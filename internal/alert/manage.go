package alert

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// --- Canaux ---

func (e *Engine) CreateChannel(ctx context.Context, definition ChannelDefinition) (Channel, error) {
	definition = definition.normalized()
	if err := definition.Validate(); err != nil {
		return Channel{}, err
	}
	if definition.Format == FormatMatrix && definition.Secret == "" {
		return Channel{}, ErrChannelTokenMissing
	}
	channel := Channel{
		Name: strings.TrimSpace(definition.Name), URL: definition.URL, Format: definition.Format, Destination: definition.Destination, Secret: definition.Secret,
		MinSeverity: definition.MinSeverity, NotifyResolve: definition.NotifyResolve, ReceivesAudit: definition.ReceivesAudit, Enabled: definition.Enabled, CreatedAt: e.now(),
	}
	id, err := e.insertChannelUnderCeiling(ctx, channel)
	if err != nil {
		return Channel{}, err
	}
	e.logger.Info("alert channel created", "channel_id", id, "name", channel.Name, "format", string(channel.Format))
	channel.ID = id
	e.auditor.Record(ctx, channelEvent(audit.ChannelCreate, channel))
	e.changed()
	return e.store.GetChannel(ctx, id)
}

func (e *Engine) insertChannelUnderCeiling(ctx context.Context, channel Channel) (int64, error) {
	e.creatingChannel.Lock()
	defer e.creatingChannel.Unlock()
	existing, err := e.store.ListChannels(ctx)
	if err != nil {
		return 0, err
	}
	if len(existing) >= MaxChannels {
		return 0, ErrTooManyChannels
	}
	return e.store.InsertChannel(ctx, channel)
}

// UpdateChannel réécrit le canal ; un secret vide garde celui en place,
// ClearSecret le retire.
func (e *Engine) UpdateChannel(ctx context.Context, id int64, definition ChannelDefinition) (Channel, error) {
	definition = definition.normalized()
	if err := definition.Validate(); err != nil {
		return Channel{}, err
	}
	channel, err := e.store.GetChannel(ctx, id)
	if err != nil {
		return Channel{}, err
	}
	// Ce qui a changé se dit sans la valeur : l'URL et le secret en sont.
	urlChanged := channel.URL != definition.URL
	channel.Name = strings.TrimSpace(definition.Name)
	channel.URL = definition.URL
	channel.Format = definition.Format
	channel.Destination = definition.Destination
	channel.MinSeverity = definition.MinSeverity
	channel.NotifyResolve = definition.NotifyResolve
	channel.ReceivesAudit = definition.ReceivesAudit
	channel.Enabled = definition.Enabled
	if definition.ClearSecret {
		channel.Secret = ""
	}
	if definition.Secret != "" {
		channel.Secret = definition.Secret
	}
	// Un bot Matrix sans jeton ne peut rien écrire : le retirer, c'est
	// supprimer le canal.
	if channel.Format == FormatMatrix && channel.Secret == "" {
		return Channel{}, ErrChannelTokenMissing
	}
	if err := e.store.UpdateChannel(ctx, channel); err != nil {
		return Channel{}, err
	}
	e.logger.Info("alert channel updated", "channel_id", id, "name", channel.Name, "enabled", channel.Enabled)
	e.auditor.Record(ctx, channelUpdateEvent(channel, urlChanged, definition.Secret != "" || definition.ClearSecret))
	e.changed()
	return e.store.GetChannel(ctx, id)
}

func (e *Engine) DeleteChannel(ctx context.Context, id int64) error {
	// Relu avant : le journal garde le nom de ce qui part.
	found, err := e.store.GetChannel(ctx, id)
	if err != nil {
		return err
	}
	if err := e.store.DeleteChannel(ctx, id); err != nil {
		return err
	}
	e.logger.Info("alert channel deleted", "channel_id", id)
	e.auditor.Record(ctx, channelEvent(audit.ChannelDelete, found))
	e.changed()
	return nil
}

// --- Fenêtres pour taire ---

// CreateMute ouvre une fenêtre, tout de suite ou à l'heure dite. La cible
// doit exister : la clé étrangère refuserait en erreur brute.
func (e *Engine) CreateMute(ctx context.Context, definition MuteDefinition) (Mute, error) {
	now := e.now()
	if err := definition.Validate(now); err != nil {
		return Mute{}, err
	}
	target := Target{Kind: definition.Target.Kind, ID: definition.Target.ID}
	if target.Kind != TargetAll {
		name, err := e.store.MuteTargetName(ctx, target)
		if errors.Is(err, ErrNotFound) {
			return Mute{}, ErrMuteTargetUnknown
		}
		if err != nil {
			return Mute{}, err
		}
		target.Name = name
	}
	startsAt, endsAt := definition.Window(now)
	mute := Mute{
		Kind: definition.Kind, Target: target, Reason: strings.TrimSpace(definition.Reason),
		StartsAt: startsAt, EndsAt: endsAt, CreatedBy: audit.ActorFrom(ctx), CreatedAt: now,
		Announce: definition.Announce, PublicTitle: strings.TrimSpace(definition.PublicTitle),
	}
	id, err := e.store.InsertMute(ctx, mute)
	if err != nil {
		return Mute{}, err
	}
	mute.ID = id
	e.logger.Info("alert mute created", "mute_id", id, "kind", string(mute.Kind), "target_kind", string(target.Kind), "target_id", target.ID,
		"from", mute.StartsAt, "until", mute.EndsAt)
	e.auditor.Record(ctx, muteEvent(audit.MuteCreate, mute))
	e.changed()
	return mute, nil
}

func (e *Engine) DeleteMute(ctx context.Context, id int64) error {
	if err := e.store.DeleteMute(ctx, id); err != nil {
		return err
	}
	e.logger.Info("alert mute deleted", "mute_id", id)
	e.auditor.Record(ctx, audit.Event{Action: audit.MuteDelete, ObjectID: strconv.FormatInt(id, 10)})
	e.changed()
	return nil
}
