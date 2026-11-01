import type { ContainerDirectory, GapsResponse, GapState, IgnoreKey, LabelProblem, ServiceGap, UndeclaredGroup } from "../api/directory";
import type { Tone } from "./tone";
import type { Translate } from "../i18n/context";
import type { SelectOption } from "./select";

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

// Ce que les machines font tourner sans que l'annuaire le déclare, en deux
// listes : les propositions (l'opérateur n'a rien dit), et les ignorés (il a
// dit que c'est voulu).
export function proposalsOf(groups: readonly UndeclaredGroup[]): UndeclaredGroup[] {
  return groups.filter((group) => !group.ignored);
}

export function ignoredOf(groups: readonly UndeclaredGroup[]): UndeclaredGroup[] {
  return groups.filter((group) => group.ignored);
}

// Des conteneurs, pas des groupes : c'est ce que l'opérateur a à régler.
export function containerCount(groups: readonly UndeclaredGroup[]): number {
  return groups.reduce((sum, group) => sum + group.containers.length, 0);
}

// « Modifier avant d'accepter » ouvre l'assistant de déclaration, que le
// serveur pré-remplit depuis ce conteneur.
export function editBeforeAcceptPath(containerID: string): string {
  return `/services/annuaire/nouveau?container=${encodeURIComponent(containerID)}`;
}

// Le conteneur par lequel un groupe s'accepte : le premier sans label, dans
// l'ordre du serveur. Un conteneur étiqueté n'a pas de proposition, le
// serveur la refuse ; le groupe ne s'accepte que par un voisin qui n'en porte
// pas.
export function acceptanceTarget(group: UndeclaredGroup): string {
  return group.containers.find((container) => container.label_service === "" && container.label_unit === "")?.id ?? "";
}

// Il y a de quoi proposer dès qu'un groupe que l'opérateur n'ignore pas est
// acceptable : c'est la règle de l'export du serveur.
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
// (aucune valeur à montrer) ou non, le motif est là : « Accepter » n'y changerait
// rien, le label continuerait de gagner.
export function isOrphan(directory: Pick<ContainerDirectory, "label_problem"> | null): boolean {
  return directory !== null && directory.label_problem !== "";
}

// La clé d'observation d'un groupe, ce qu'on ignore ou cesse d'ignorer.
export function ignoreKeyOf(group: UndeclaredGroup): IgnoreKey {
  return { machine_id: group.machine_id, kind: group.key_kind, value: group.key_value };
}

// Les conteneurs que l'assistant « Déclarer un service » propose de choisir :
// un par groupe qu'on peut accepter (un service Compose, un conteneur seul),
// avec son projet Compose et sa machine pour les distinguer. Un groupe ignoré
// ou d'orphelins n'y est pas : l'assistant ne rouvrirait que ce que « Accepter »
// refuse.
export function pickableContainers(groups: readonly UndeclaredGroup[]): SelectOption[] {
  return proposalsOf(groups)
    .filter((group) => group.adoptable)
    .map((group) => ({
      value: acceptanceTarget(group),
      label: group.compose_service === "" ? group.key_value : `${group.compose_service} (${group.project})`,
      hint: group.machine_name,
    }))
    .filter((option) => option.value !== "")
    .sort((a, b) => a.label.localeCompare(b.label));
}
