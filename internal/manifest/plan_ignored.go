package manifest

import (
	"github.com/ldesfontaine/opencloud/internal/directory"
)

func ignoredName(machine string, kind directory.IgnoreKind, value string) string {
	return machine + "/" + string(kind) + ":" + value
}

func (b *builder) planIgnored() {
	present := make(map[directory.IgnoredKey]bool, len(b.current.Ignored))
	for _, key := range b.current.Ignored {
		present[ignoredIdentity(key)] = true
	}
	wantedKeys := map[directory.IgnoredKey]bool{}
	for _, entry := range b.file.Ignored {
		name := ignoredName(entry.Machine, entry.Kind, entry.Value)
		machineID, known := b.machineID(entry.Machine, entry.Line, name)
		if !known {
			continue
		}
		key := directory.IgnoredKey{MachineID: machineID, Kind: entry.Kind, Value: entry.Value}.Complete()
		identity := ignoredIdentity(key)
		if wantedKeys[identity] {
			b.refuse(KeyNameDuplicate, entry.Line, name, name)
			continue
		}
		wantedKeys[identity] = true
		if present[identity] {
			b.unchanged++
			continue
		}
		if err := key.Validate(); err != nil {
			b.refuseError(err, entry.Line, name)
			continue
		}
		change := Change{Action: ActionCreate, Kind: KindIgnored, Name: name}
		b.ignoredCreates = append(b.ignoredCreates, step{change: change, ignored: ignoredStep{Key: key}})
	}
	for _, key := range b.current.Ignored {
		if wantedKeys[ignoredIdentity(key)] {
			continue
		}
		name := ignoredName(b.names.machine(key.MachineID), key.Kind, key.Value)
		if !b.prune {
			b.unmanaged = append(b.unmanaged, Ref{Kind: KindIgnored, Name: name})
			continue
		}
		change := Change{Action: ActionDelete, Kind: KindIgnored, Name: name}
		b.ignoredDeletes = append(b.ignoredDeletes, step{change: change, ignored: ignoredStep{Key: key}})
	}
}

// ignoredIdentity est la clé ignorée sans sa date : la seule chose qui la
// distingue.
func ignoredIdentity(key directory.IgnoredKey) directory.IgnoredKey {
	return directory.IgnoredKey{MachineID: key.MachineID, Kind: key.Kind, Value: key.Value}
}
