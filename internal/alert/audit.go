package alert

import (
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (e *Engine) SetAuditor(auditor *audit.Recorder) {
	e.auditor = auditor
}

func acknowledgeEvent(acknowledged Alert) audit.Event {
	return audit.Event{
		Action: audit.AlertAcknowledge, ObjectID: strconv.FormatInt(acknowledged.ID, 10), ObjectName: acknowledged.Object.Name,
		Details: audit.Details{"kind": string(acknowledged.Kind), "object_kind": string(acknowledged.Object.Kind), "object_id": acknowledged.Object.ID},
	}
}

// acknowledgeManyEvent : un seul événement pour la sélection, qui dit ce qui
// était demandé ; la base ne rend que le compte de ce qui a changé.
func acknowledgeManyEvent(requested []int64, acknowledged int) audit.Event {
	ids := make([]string, 0, len(requested))
	for _, id := range requested {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	return audit.Event{
		Action: audit.AlertAcknowledge,
		Details: audit.Details{
			"alert_ids":    strings.Join(ids, ","),
			"acknowledged": strconv.Itoa(acknowledged),
		},
	}
}

// channelEvent ne dit jamais l'URL ni le secret : l'une et l'autre
// suffisent à écrire dans le salon.
func channelEvent(action audit.Action, written Channel) audit.Event {
	return audit.Event{
		Action: action, ObjectID: strconv.FormatInt(written.ID, 10), ObjectName: written.Name,
		Details: audit.Details{
			"format":         string(written.Format),
			"min_severity":   string(written.MinSeverity),
			"notify_resolve": strconv.FormatBool(written.NotifyResolve),
			"enabled":        strconv.FormatBool(written.Enabled),
		},
	}
}

func channelUpdateEvent(written Channel, urlChanged, secretChanged bool) audit.Event {
	event := channelEvent(audit.ChannelUpdate, written)
	event.Details["url_changed"] = strconv.FormatBool(urlChanged)
	event.Details["secret_changed"] = strconv.FormatBool(secretChanged)
	return event
}

func muteEvent(action audit.Action, written Mute) audit.Event {
	return audit.Event{
		Action: action, ObjectID: strconv.FormatInt(written.ID, 10), ObjectName: written.Target.Name,
		Details: audit.Details{
			"kind":        string(written.Kind),
			"target_kind": string(written.Target.Kind),
			"target_id":   written.Target.ID,
			"reason":      written.Reason,
			"starts_at":   written.StartsAt.UTC().Format(time.RFC3339),
			"ends_at":     written.EndsAt.UTC().Format(time.RFC3339),
		},
	}
}
