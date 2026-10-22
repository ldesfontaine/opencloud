import type { ImageCheck, Container } from "../api/containers";
import type { ContainerDirectory, DeployPlan } from "../api/directory";
import type { Translate } from "../i18n/context";

type Checked = Pick<Container, "update_policy" | "image_check">;

// Une mise à jour se montre quand le constat en nomme une et que
// l'opérateur ne l'a pas épinglée (hold) ; la même règle que le compteur.
export function updateAvailable(container: Checked): boolean {
  return container.update_policy !== "hold" && container.image_check !== null && container.image_check.kind !== "";
}

// Ce que la pastille écrit : le tag plus récent, ou « reconstruite »
// quand c'est le même tag dont l'image a bougé.
export function updateLabel(t: Translate, check: ImageCheck): string {
  return check.kind === "digest" ? t("update.pill_digest") : check.newer_tag;
}

// La phrase du constat : la mise à jour et son type, ou pourquoi il n'y
// en a pas.
export function checkText(t: Translate, check: ImageCheck): string {
  if (check.outcome !== "ok") {
    return t(`update.outcome_${check.outcome}`);
  }
  if (check.kind === "") {
    return t("update.up_to_date");
  }
  return t(`update.available_${check.kind}`, check.target);
}

// Sous Compose, un tag plus récent se change d'abord dans le fichier : la
// commande tire ce que le fichier écrit.
export function needsComposeEdit(container: Pick<Container, "compose_service" | "compose_dir">, check: ImageCheck): boolean {
  return container.compose_service !== "" && container.compose_dir !== "" && check.newer_tag !== "";
}

// La ligne image du fichier Compose, avant et après : ce que l'opérateur
// change dans son dépôt. Docker rend l'image une fois les variables du
// fichier résolues : la ligne d'avant peut s'y écrire autrement.
export interface LineChange {
  before: string;
  after: string;
}

export function imageLineChange(container: Pick<Container, "compose_service" | "compose_dir" | "image">, check: ImageCheck): LineChange | null {
  if (!needsComposeEdit(container, check) || check.target === "") {
    return null;
  }
  return { before: `image: ${container.image}`, after: `image: ${check.target}` };
}

// L'empreinte courte, comme Docker l'écrit.
export function shortDigest(digest: string): string {
  return digest.replace("sha256:", "").slice(0, 12);
}

// Le service dont il faut lire le plan de déploiement pour savoir si le
// bouton « Passer à » paraît : l'unité est déclarée, suivie (watch), et le
// constat propose un tag plus récent. Une empreinte changée sous le même tag
// n'en est pas un : `--pull missing` ne la tirerait pas. null : pas de
// requête à faire.
export function upgradeServiceID(container: Checked, directory: ContainerDirectory | null): string | null {
  if (directory === null || !directory.declared || container.update_policy !== "watch") {
    return null;
  }
  const check = container.image_check;
  if (check === null || check.newer_tag === "" || check.kind === "" || check.kind === "digest") {
    return null;
  }
  return directory.service_id;
}

// L'offre du bouton : la version que l'agent propose, pour un service qu'un
// ordre signé a déjà déployé (le plan le dit). Sinon, la carte garde
// la ligne à copier.
export function upgradeOffer(container: Checked, directory: ContainerDirectory | null, plan: Pick<DeployPlan, "deployed"> | null): { version: string } | null {
  if (upgradeServiceID(container, directory) === null || plan === null || !plan.deployed || container.image_check === null) {
    return null;
  }
  return { version: container.image_check.newer_tag };
}
