import type { DeclaredService, GapState, ServiceGap } from "../api/directory";

// Un même service sur plusieurs machines : mêmes nom et mêmes images. Il se
// dérive, il ne s'écrit pas : renommer un service, ou changer son image sur
// une machine, le sort du groupe.
export interface InstanceGroup {
  key: string;
  name: string;
  instances: DeclaredService[];
}

// Les services d'un projet Compose (le groupe d'exécution, lu et non déclaré).
// Project vide : ceux qui n'ont pas de projet Compose.
interface ProjectSection {
  project: string;
  groups: InstanceGroup[];
}

function instanceKey(service: DeclaredService): string {
  const images = service.units.map((unit) => unit.image).join(",");
  return `${service.name}\n${images}`;
}

// Les services regroupés par instance, puis par projet Compose (celui de la
// première instance). Les projets par nom, « sans projet » en dernier ; dans
// un projet, les services par nom ; dans un groupe, les instances par machine.
export function groupServices(services: readonly DeclaredService[]): ProjectSection[] {
  const groups = new Map<string, InstanceGroup>();
  for (const service of services) {
    const key = instanceKey(service);
    const group = groups.get(key);
    if (group === undefined) {
      groups.set(key, { key, name: service.name, instances: [service] });
    } else {
      group.instances.push(service);
    }
  }
  const sections = new Map<string, InstanceGroup[]>();
  for (const group of groups.values()) {
    group.instances.sort((a, b) => a.machine_name.localeCompare(b.machine_name));
    const project = group.instances[0]?.compose_project ?? "";
    sections.set(project, [...(sections.get(project) ?? []), group]);
  }
  return [...sections.entries()]
    .sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
    .map(([project, projectGroups]) => ({ project, groups: projectGroups.sort((a, b) => a.name.localeCompare(b.name)) }));
}

// L'ordre de gravité d'un écart, du plus grave au moins grave : la ligne
// d'un groupe montre le pire de ses instances.
const gravity: readonly GapState[] = ["absent", "diverged", "unverifiable", "unobserved", "conform"];

// Le pire écart des instances qui en ont un ; undefined si aucune n'en a.
export function worstGap(instances: readonly DeclaredService[], gaps: ReadonlyMap<string, ServiceGap>): ServiceGap | undefined {
  let worst: ServiceGap | undefined;
  for (const instance of instances) {
    const gap = gaps.get(instance.id);
    if (gap !== undefined && (worst === undefined || gravity.indexOf(gap.state) < gravity.indexOf(worst.state))) {
      worst = gap;
    }
  }
  return worst;
}
