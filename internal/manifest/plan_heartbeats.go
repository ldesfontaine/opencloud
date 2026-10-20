package manifest

import (
	"strings"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

func (b *builder) planHeartbeats() {
	entries := make([]entryRef, 0, len(b.file.Heartbeats))
	for _, entry := range b.file.Heartbeats {
		entries = append(entries, entryRef{id: entry.ID, name: strings.TrimSpace(entry.Name), line: entry.Line})
	}
	objects := make([]objectRef, 0, len(b.current.Heartbeats))
	for _, item := range b.current.Heartbeats {
		objects = append(objects, objectRef{id: item.ID, name: item.Name})
	}
	matched := b.match(entries, objects, false)

	for index, entry := range b.file.Heartbeats {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		b.planHeartbeat(entry, found)
	}
	for index, item := range b.current.Heartbeats {
		if matched.claimed[index] {
			continue
		}
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindHeartbeat, ID: item.ID, Name: item.Name})
			continue
		}
		change := Change{Action: ActionDelete, Kind: KindHeartbeat, ID: item.ID, Name: item.Name}
		b.heartbeatDeletes = append(b.heartbeatDeletes, step{change: change, heartbeat: heartbeatStep{Before: item.Definition()}})
	}
}

func (b *builder) planHeartbeat(entry Heartbeat, found int) {
	machineID := ""
	if entry.Machine != "" {
		var known bool
		if machineID, known = b.machineID(entry.Machine, entry.Line, entry.Name); !known {
			return
		}
	}
	wanted := heartbeat.Definition{Name: strings.TrimSpace(entry.Name), MachineID: machineID, Interval: entry.Every, Grace: entry.Grace}
	if found == createEntry {
		if err := wanted.Validate(); err != nil {
			b.refuseError(err, entry.Line, entry.Name)
			return
		}
		fields := afterOnly(diffHeartbeat(heartbeat.Definition{}, wanted, b.names))
		// Le jeton n'est jamais dans le fichier : le serveur le tire.
		change := Change{Action: ActionCreate, Kind: KindHeartbeat, Name: wanted.Name, Fields: fields, Notes: []Note{NoteTokenGenerated}}
		b.heartbeatCreates = append(b.heartbeatCreates, step{change: change, heartbeat: heartbeatStep{Wanted: wanted}})
		return
	}
	existing := b.current.Heartbeats[found]
	before := existing.Definition()
	fields := diffHeartbeat(before, wanted, b.names)
	if len(fields) == 0 {
		b.unchanged++
		return
	}
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, entry.Name)
		return
	}
	change := Change{Action: ActionUpdate, Kind: KindHeartbeat, ID: existing.ID, Name: wanted.Name, Fields: fields}
	rhythmChanged := before.Interval != wanted.Interval || before.Grace != wanted.Grace
	if rhythmChanged && !existing.IsPaused() {
		change.Notes = []Note{NoteHeartbeatDeadlineMoves}
	}
	b.heartbeatUpdates = append(b.heartbeatUpdates, step{change: change, heartbeat: heartbeatStep{Wanted: wanted, Before: before}})
}
