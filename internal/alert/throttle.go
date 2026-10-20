package alert

import (
	"context"
	"slices"
	"time"
)

// Held est ce qu'un résumé remplace : combien d'envois la limite d'un canal
// a retenus, la gravité la plus haute parmi eux, et les machines qui les
// ont causés. Un résumé qui taisait la gravité ferait passer pour banal ce
// qu'une machine bruyante aurait masqué.
type Held struct {
	Count    int
	Severity Severity
	// Machines : les noms, triés, au plus MaxSummaryMachines ; Others compte
	// celles qui ne tiennent pas dans la liste.
	Machines []string
	Others   int
}

type shareKey struct {
	channelID int64
	machineID string
}

func (e *Engine) limitOf(channelID int64) *channelLimit {
	limit, found := e.limits[channelID]
	if !found {
		limit = &channelLimit{}
		e.limits[channelID] = limit
	}
	return limit
}

// allowSend accorde un envoi à une machine sur un canal, ou le refuse. Deux
// plafonds : la part de la machine, qu'elle seule épuise, et celui du
// canal. Une machine qui n'a rien envoyé sur la fenêtre peut dépasser le
// plafond du canal de ChannelReserve : la première alerte d'une machine
// calme n'est pas retenue parce que d'autres ont rempli le canal.
func (e *Engine) allowSend(limit *channelLimit, channelID int64, machineID string, now time.Time) bool {
	key := shareKey{channelID: channelID, machineID: machineID}
	share, found := e.shares[key]
	if !found {
		share = &sendWindow{}
		e.shares[key] = share
	}
	used := share.count(now)
	if used >= MachineRateLimit {
		return false
	}
	ceiling := ChannelRateLimit
	if used == 0 {
		ceiling += ChannelReserve
	}
	if limit.window.count(now) >= ceiling {
		return false
	}
	share.add(now)
	limit.window.add(now)
	return true
}

// flushSummary envoie le résumé de ce que la limite a retenu, dès que la
// fenêtre du canal a de la place : un message, quel que soit le nombre.
// Il n'est la livraison d'aucune alerte, rien n'en reste en base.
func (e *Engine) flushSummary(limit *channelLimit, channel Channel, now time.Time) {
	// Le résumé attend que la fenêtre ait de la place, pour le canal et pour
	// chaque machine retenue : tant qu'une machine bruyante est à sa limite, la
	// tempête dure et un résumé par alerte retenue serait un envoi de plus.
	// Il prend une place du canal, mais pas dans la réserve : elle est aux
	// machines calmes.
	if limit.held.count == 0 || limit.window.count(now) >= ChannelRateLimit || !e.sharesHaveRoom(channel.ID, limit.held, now) {
		return
	}
	limit.window.add(now)
	held := limit.held.summary()
	limit.held = heldAlerts{}
	e.logger.Info("alert deliveries summarized", "channel_id", channel.ID, "held", held.Count, "severity", string(held.Severity))
	e.sender.Enqueue(Job{Delivery: Delivery{ChannelID: channel.ID, Event: EventSummary, Status: DeliveryPending, CreatedAt: now, UpdatedAt: now}, Channel: channel, Held: held})
}

func (e *Engine) sharesHaveRoom(channelID int64, held heldAlerts, now time.Time) bool {
	for machineID := range held.machines {
		share, found := e.shares[shareKey{channelID: channelID, machineID: machineID}]
		if found && share.count(now) >= MachineRateLimit {
			return false
		}
	}
	return true
}

// flushSummaries passe sur chaque canal qui a retenu des envois ; le
// balayage l'appelle, une tempête finie se résume sans attendre une
// nouvelle alerte.
func (e *Engine) flushSummaries(ctx context.Context) {
	if e.sender == nil {
		return
	}
	pending := false
	for _, limit := range e.limits {
		pending = pending || limit.held.count > 0
	}
	if !pending {
		return
	}
	channels, err := e.store.ListChannels(ctx)
	if err != nil {
		e.fail(ctx, "list channels", err)
		return
	}
	now := e.now()
	for _, channel := range channels {
		if limit, found := e.limits[channel.ID]; found && channel.Enabled {
			e.flushSummary(limit, channel, now)
		}
	}
}

// sendWindow compte des envois sur une fenêtre glissante.
type sendWindow struct {
	sent []time.Time
}

// count oublie les envois sortis de la fenêtre et dit combien restent.
func (w *sendWindow) count(now time.Time) int {
	kept := w.sent[:0]
	for _, at := range w.sent {
		if at.After(now.Add(-ChannelRateWindow)) {
			kept = append(kept, at)
		}
	}
	w.sent = kept
	return len(w.sent)
}

func (w *sendWindow) add(now time.Time) {
	w.sent = append(w.sent, now)
}

// channelLimit tient les envois d'un canal sur la fenêtre, et ce que la
// limite a retenu depuis le dernier résumé.
type channelLimit struct {
	window sendWindow
	held   heldAlerts
}

// heldAlerts note ce que la limite retient, pour le résumé.
type heldAlerts struct {
	count    int
	severity Severity
	// machines : nom par identifiant, pour ne compter chaque machine qu'une
	// fois. Une alerte sans machine y entre sous l'identifiant vide, sans nom.
	machines map[string]string
}

func (h *heldAlerts) note(alert Alert) {
	h.count++
	if h.severity == "" || rank(alert.Severity) > rank(h.severity) {
		h.severity = alert.Severity
	}
	if h.machines == nil {
		h.machines = map[string]string{}
	}
	name := alert.MachineName
	if name == "" {
		name = alert.MachineID
	}
	h.machines[alert.MachineID] = name
}

func (h heldAlerts) summary() Held {
	names := make([]string, 0, len(h.machines))
	for _, name := range h.machines {
		if name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	held := Held{Count: h.count, Severity: h.severity, Machines: names}
	if len(names) > MaxSummaryMachines {
		held.Machines, held.Others = names[:MaxSummaryMachines], len(names)-MaxSummaryMachines
	}
	return held
}

// forgetIdleShares oublie les parts vides : une machine retirée ne laisse
// rien en mémoire, et une machine calme repart d'une fenêtre neuve.
func (e *Engine) forgetIdleShares(now time.Time) {
	for key, share := range e.shares {
		if share.count(now) == 0 {
			delete(e.shares, key)
		}
	}
}
