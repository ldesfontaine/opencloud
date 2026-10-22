package status

// exists dit si l'objet est encore connu ; un objet disparu ne touche rien.
func (f facts) exists(kind MemberKind, id string) bool {
	switch kind {
	case KindMachine:
		_, ok := f.machines[id]
		return ok
	case KindService:
		_, ok := f.declared[id]
		return ok
	case KindHeartbeat:
		_, ok := f.heartbeats[id]
		return ok
	case KindProbe:
		_, ok := f.probes[id]
		return ok
	}
	return false
}

// touches dit si une cible touche un membre de composant. Jamais la machine
// qui lance une sonde : une sonde qui regarde de dehors reste juste ; ni un
// service sa machine : la panne d'une application n'est pas celle du serveur.
// Les incidents et les silences annoncés suivent la même règle.
func (f facts) touches(target MemberRef, member Member) bool {
	if !f.exists(target.Kind, target.ID) || !f.exists(member.Kind, member.ID) {
		return false
	}
	switch target.Kind {
	case KindMachine:
		return f.machineTouches(target.ID, member)
	case KindService:
		return f.serviceTouches(target.ID, member)
	case KindHeartbeat:
		return member.Kind == KindHeartbeat && member.ID == target.ID
	case KindProbe:
		return member.Kind == KindProbe && member.ID == target.ID
	}
	return false
}

// machineTouches : la machine, ses services, ses tâches, et les sondes de
// ses services.
func (f facts) machineTouches(machineID string, member Member) bool {
	switch member.Kind {
	case KindMachine:
		return member.ID == machineID
	case KindService:
		return f.declared[member.ID].MachineID == machineID
	case KindHeartbeat:
		return f.heartbeats[member.ID].MachineID == machineID
	case KindProbe:
		serviceID := f.probes[member.ID].ServiceID
		if serviceID == "" {
			return false
		}
		return f.declared[serviceID].MachineID == machineID
	}
	return false
}

// serviceTouches : le service, et les sondes qui le regardent.
func (f facts) serviceTouches(serviceID string, member Member) bool {
	switch member.Kind {
	case KindService:
		return member.ID == serviceID
	case KindProbe:
		return f.probes[member.ID].ServiceID == serviceID
	}
	return false
}

// touchedComponents rend les composants dont l'un des membres est touché
// par l'une des cibles, dans l'ordre des composants, chacun une fois.
func (f facts) touchedComponents(targets []MemberRef, components []Component) []ComponentRef {
	touched := []ComponentRef{}
	for _, component := range components {
		if f.componentIsTouched(targets, component) {
			touched = append(touched, ComponentRef{ID: component.ID, Name: component.Name})
		}
	}
	return touched
}

func (f facts) componentIsTouched(targets []MemberRef, component Component) bool {
	for _, member := range component.Members {
		for _, target := range targets {
			if f.touches(target, member) {
				return true
			}
		}
	}
	return false
}

// nameTargets pose le nom de chaque objet visé, comme pour un membre.
func (f facts) nameTargets(targets []Target) {
	for i := range targets {
		member := Member{Kind: targets[i].Kind, ID: targets[i].ID}
		f.fill(&member)
		targets[i].Name = member.Name
	}
}

// describe pose, sur chaque incident, le nom de ses cibles et les
// composants qu'elles touchent parmi ceux donnés.
func (f facts) describe(incidents []Incident, components []Component) {
	for i := range incidents {
		f.nameTargets(incidents[i].Targets)
		incidents[i].Components = f.touchedComponents(refsOf(incidents[i].Targets), components)
	}
}

func refsOf(targets []Target) []MemberRef {
	refs := make([]MemberRef, 0, len(targets))
	for _, target := range targets {
		refs = append(refs, MemberRef{Kind: target.Kind, ID: target.ID})
	}
	return refs
}

func targetsOf(refs []MemberRef) []Target {
	targets := make([]Target, 0, len(refs))
	for _, ref := range refs {
		targets = append(targets, Target{Kind: ref.Kind, ID: ref.ID})
	}
	return targets
}
