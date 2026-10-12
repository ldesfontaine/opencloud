import type { ImageCheck, Service } from "../api/types";
import type { Translate } from "../i18n/context";

type Checked = Pick<Service, "update_policy" | "image_check">;

// Une mise à jour se montre quand le constat en nomme une et que
// l'opérateur ne l'a pas épinglée ; la même règle que le compteur.
export function updateAvailable(service: Checked): boolean {
  return service.update_policy !== "pinned" && service.image_check !== null && service.image_check.kind !== "";
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
export function needsComposeEdit(service: Pick<Service, "compose_service" | "compose_dir">, check: ImageCheck): boolean {
  return service.compose_service !== "" && service.compose_dir !== "" && check.newer_tag !== "";
}

// La ligne image du fichier Compose, avant et après : ce que l'opérateur
// change dans son dépôt. Docker rend l'image une fois les variables du
// fichier résolues : la ligne d'avant peut s'y écrire autrement.
export interface LineChange {
  before: string;
  after: string;
}

export function imageLineChange(service: Pick<Service, "compose_service" | "compose_dir" | "image">, check: ImageCheck): LineChange | null {
  if (!needsComposeEdit(service, check) || check.target === "") {
    return null;
  }
  return { before: `image: ${service.image}`, after: `image: ${check.target}` };
}

// L'empreinte courte, comme Docker l'écrit.
export function shortDigest(digest: string): string {
  return digest.replace("sha256:", "").slice(0, 12);
}
