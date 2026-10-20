import type { ContainerDirectory, GapsResponse, GapState, IgnoreKey, LabelProblem, ServiceGap, UndeclaredGroup } from "../api/directory";
import type { Tone } from "./tone";
import type { Translate } from "../i18n/context";

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

// Le conteneur par lequel un groupe s'adopte : le premier sans label, dans
// l'ordre du serveur. Un conteneur étiqueté ne s'adopte pas, le serveur le
// refuse ; le groupe ne s'adopte que par un voisin qui n'en porte pas.
export function adoptionTarget(group: UndeclaredGroup): string {
  return group.containers.find((container) => container.label_service === "" && container.label_unit === "")?.id ?? "";
}

// Il y a de quoi proposer dès qu'un groupe que l'opérateur n'ignore pas est
// adoptable : c'est la règle de l'export du serveur.
export function hasProposals(groups: readonly UndeclaredGroup[]): boolean {
  return groups.some((group) => !group.ignored && group.adoptable);
}

// La clé du catalogue qui dit pourquoi un label ne rattache rien.
export function orphanKey(problem: LabelProblem): string {
  return `gap.orphan_${problem}`;
}

// Le motif d'un label orphelin sur la fiche d'un conteneur. Un service
// déclaré sur une autre machine le dit, avec son nom : un label ne rattache
// que les conteneurs de sa propre machine.
export function orphanText(t: Translate, directory: ContainerDirectory): string {
  if (directory.label_problem === "") {
    return "";
  }
  if (directory.label_problem === "service_unknown" && directory.label_machine_name !== "") {
    return t("gap.orphan_elsewhere", directory.label_machine_name);
  }
  return t(orphanKey(directory.label_problem));
}

// Le conteneur porte-t-il des labels qui ne le rattachent à rien ? Mal formés
// (aucune valeur à montrer) ou non, le motif est là : « Adopter » n'y changerait
// rien, le label continuerait de gagner.
export function isOrphan(directory: Pick<ContainerDirectory, "label_problem"> | null): boolean {
  return directory !== null && directory.label_problem !== "";
}

// La clé d'observation d'un groupe, ce qu'on ignore ou cesse d'ignorer.
export function ignoreKeyOf(group: UndeclaredGroup): IgnoreKey {
  return { machine_id: group.machine_id, kind: group.key_kind, value: group.key_value };
}
