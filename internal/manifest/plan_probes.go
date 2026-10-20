package manifest

import (
	"strings"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

func (b *builder) planProbes() {
	entries := make([]entryRef, 0, len(b.file.Probes))
	for _, entry := range b.file.Probes {
		entries = append(entries, entryRef{id: entry.ID, name: strings.TrimSpace(entry.Name), line: entry.Line})
	}
	objects := make([]objectRef, 0, len(b.current.Probes))
	for _, item := range b.current.Probes {
		objects = append(objects, objectRef{id: item.ID, name: item.Name})
	}
	matched := b.match(entries, objects, false)

	perMachine := map[string]int{}
	lastLine := map[string]int{}
	for index, entry := range b.file.Probes {
		found := matched.existing[index]
		if found == refusedEntry {
			continue
		}
		runnerID, ok := b.planProbe(entry, found)
		if ok {
			perMachine[runnerID]++
			lastLine[runnerID] = entry.Line
		}
	}
	for index, item := range b.current.Probes {
		if matched.claimed[index] {
			continue
		}
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindProbe, ID: item.ID, Name: item.Name})
			perMachine[item.MachineID]++
			continue
		}
		change := Change{Action: ActionDelete, Kind: KindProbe, ID: item.ID, Name: item.Name}
		b.probeDeletes = append(b.probeDeletes, step{change: change, probe: probeStep{Before: item.Definition().Complete()}})
	}
	b.checkProbeCeiling(perMachine, lastLine)
}

// checkProbeCeiling refuse un plan qui laisserait une machine avec plus de
// sondes que le domaine n'en admet : mieux vaut le dire au plan qu'au
// milieu de l'application.
func (b *builder) checkProbeCeiling(perMachine, lastLine map[string]int) {
	for machineID, count := range perMachine {
		if count > probe.MaxProbesPerMachine {
			b.refuseError(probe.ErrTooMany, lastLine[machineID], b.names.machine(machineID))
		}
	}
}

// planProbe juge une entrée de sonde ; elle rend la machine qui sonde, pour le
// plafond, et faux quand l'entrée est refusée.
func (b *builder) planProbe(entry Probe, found int) (runnerID string, ok bool) {
	wanted, serviceRef, ok := b.wantedProbe(entry)
	if !ok {
		return "", false
	}
	runnerID = wanted.MachineID
	if found == createEntry {
		if err := wanted.Validate(); err != nil {
			b.refuseError(err, entry.Line, entry.Name)
			return runnerID, false
		}
		fields := afterOnly(diffProbe(probe.Definition{}, wanted, b.names))
		change := Change{Action: ActionCreate, Kind: KindProbe, Name: wanted.Name, Fields: fields}
		b.probeCreates = append(b.probeCreates, step{change: change, probe: probeStep{Wanted: wanted, ServiceRef: serviceRef}})
		return runnerID, true
	}
	existing := b.current.Probes[found]
	fields := diffProbe(existing.Definition().Complete(), wanted, b.names)
	if len(fields) == 0 {
		b.unchanged++
		return runnerID, true
	}
	if err := wanted.Validate(); err != nil {
		b.refuseError(err, entry.Line, entry.Name)
		return runnerID, false
	}
	change := Change{Action: ActionUpdate, Kind: KindProbe, ID: existing.ID, Name: wanted.Name, Fields: fields}
	if wanted.ResetsState(existing) {
		change.Notes = []Note{NoteProbeResets}
	}
	if probe.TargetPassword(existing.Target) != probe.TargetPassword(wanted.Target) {
		change.Notes = append(change.Notes, NotePasswordChanged)
	}
	b.probeUpdates = append(b.probeUpdates, step{change: change, probe: probeStep{
		Wanted: wanted, ServiceRef: serviceRef, Before: existing.Definition().Complete(),
	}})
	return runnerID, true
}

// wantedProbe traduit l'entrée en définition du domaine : la machine qui
// sonde par son identifiant, le service par celui de l'état visé, réel ou
// en attente.
func (b *builder) wantedProbe(entry Probe) (definition probe.Definition, ref string, ok bool) {
	ok = true
	runner := entry.Runner
	if runner == "" {
		runner = b.current.Machines[b.current.LocalMachineID]
	}
	runnerID, known := b.machineID(runner, entry.Line, entry.Name)
	if !known {
		ok = false
	}
	serviceID := ""
	if entry.Service != "" {
		machine, name, found := strings.Cut(entry.Service, "/")
		if !found || machine == "" || name == "" || strings.Contains(name, "/") {
			b.refuse(KeyReferenceBroken, entry.Line, entry.Name, entry.Service)
			ok = false
		} else if id, exists := b.serviceTargets[entry.Service]; exists {
			serviceID, ref = id, entry.Service
		} else {
			b.refuse(KeyServiceUnknown, entry.Line, entry.Name, entry.Service)
			ok = false
		}
	}
	definition = probe.Definition{
		Name: entry.Name, Kind: entry.Kind, Target: entry.Target, MachineID: runnerID, ServiceID: serviceID,
		Interval: entry.Interval, Timeout: entry.Timeout,
		FailureThreshold: entry.FailureThreshold, RecoveryThreshold: entry.RecoveryThreshold,
		Method: entry.Method, ExpectedStatus: entry.ExpectedStatus, ExpectedBody: entry.ExpectedBody,
		FollowRedirects: entry.FollowRedirects, TLS: entry.TLS,
	}
	return definition.Complete(), ref, ok
}
