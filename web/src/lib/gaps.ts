import type { GapsResponse, GapState, ServiceGap, UndeclaredGroup } from "../api/types";
import type { Tone } from "../components/Pill";

// La teinte de l'écart : conforme se tait, un conteneur qui diverge ou qui
// manque se voit, ce qu'on ne peut pas vérifier reste discret.
export function gapTone(state: GapState): Tone {
  switch (state) {
    case "conform":
      return "ok";
    case "absent":
      return "danger";
    case "diverged":
    case "unverifiable":
      return "warn";
    case "unobserved":
      return "neutral";
  }
}

// Le filtre passe dans le chemin : changer de machine ou de projet relit
// l'écart, que le serveur calcule pour ce filtre.
export function gapsPath(machineID: string, projectID: string): string {
  const query = new URLSearchParams();
  if (machineID !== "") {
    query.set("machine", machineID);
  }
  if (projectID !== "") {
    query.set("project", projectID);
  }
  const text = query.toString();
  return text === "" ? "/api/directory/gaps" : `/api/directory/gaps?${text}`;
}

// L'écart de chaque service déclaré, par identifiant, pour le tableau.
export function gapsByService(gaps: GapsResponse | null): Map<string, ServiceGap> {
  return new Map((gaps?.services ?? []).map((gap) => [gap.service_id, gap]));
}

// Les groupes non déclarés visibles : ceux que l'opérateur n'a pas ignorés,
// ou tous quand il demande à les voir.
export function shownUndeclared(groups: readonly UndeclaredGroup[], showIgnored: boolean): UndeclaredGroup[] {
  return groups.filter((group) => showIgnored || !group.ignored);
}

export function ignoredCount(groups: readonly UndeclaredGroup[]): number {
  return groups.filter((group) => group.ignored).length;
}

// Des conteneurs, pas des groupes : c'est ce que l'opérateur a à régler.
export function containerCount(groups: readonly UndeclaredGroup[]): number {
  return groups.reduce((sum, group) => sum + group.containers.length, 0);
}

// « Adopter » ouvre le formulaire de déclaration, que le serveur pré-remplit
// depuis ce conteneur.
export function adoptPath(containerID: string): string {
  return `/services/annuaire/nouveau?container=${encodeURIComponent(containerID)}`;
}

export function adoptIntoPath(serviceID: string, containerID: string): string {
  return `/services/annuaire/${serviceID}/modifier?container=${encodeURIComponent(containerID)}`;
}

// Le conteneur par lequel un groupe s'adopte : le premier, dans l'ordre du serveur.
export function adoptionTarget(group: UndeclaredGroup): string {
  return group.containers[0]?.id ?? "";
}

// La requête d'ignorance : la clé d'observation, pas un id Docker.
export function ignoreBody(group: UndeclaredGroup) {
  return { machine_id: group.machine_id, kind: group.key_kind, value: group.key_value };
}

export function unignorePath(group: UndeclaredGroup): string {
  const query = new URLSearchParams({ machine: group.machine_id, kind: group.key_kind, value: group.key_value });
  return `/api/directory/ignored?${query.toString()}`;
}
