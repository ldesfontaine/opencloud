import type { Engine, Finding, Port, Container, ContainerState, Transition } from "../api/containers";
import type { Tone } from "./tone";
import type { Translate } from "../i18n/context";
import { formatDuration, since } from "./time";

// Les pastilles de la direction artistique, dérivées des faits : l'état
// Docker, le code de sortie, la santé. La même règle que le serveur pour
// « demande attention ».
export type PillKey = "active" | "starting" | "failing" | "out_of_memory" | "killed" | "restarting" | "paused" | "stopped" | "created" | "unknown";

export interface ContainerPillInfo {
  key: PillKey;
  tone: Tone;
}

// 0 est une fin normale, 137 et 143 un arrêt demandé (docker stop).
export function isCleanExit(code: number): boolean {
  return code === 0 || code === 137 || code === 143;
}

// Les faits dont la pastille dépend ; oom_killed, killed et stop_requested manquent sur
// une fiche d'avant leur correctif. observed manque sur une transition, qui
// est un fait passé : elle se lit toujours.
export type PillFacts = Pick<Container, "state" | "exit_code" | "health"> & { oom_killed?: boolean; killed?: boolean; stop_requested?: boolean; observed?: boolean };

// L'état que l'interface montre d'un conteneur : « Inconnu » dès que sa
// machine ne signale plus (observed faux, décidé par le serveur), l'état
// que disent les faits sinon. Tout ce qui montre l'état d'un conteneur, ou
// le compte, passe par ici ; containerPill ne lit que les faits, pour les
// transitions et le « dernier état ».
export function displayedState(container: PillFacts): ContainerPillInfo {
  if (container.observed === false) {
    return { key: "unknown", tone: "neutral" };
  }
  return containerPill(container);
}

// Ce que disent les faits seuls, sans se demander si la machine répond.
export function containerPill(container: PillFacts): ContainerPillInfo {
  switch (container.state) {
    case "running":
      if (container.health === "unhealthy") {
        return { key: "failing", tone: "danger" };
      }
      if (container.health === "starting") {
        return { key: "starting", tone: "accent" };
      }
      return { key: "active", tone: "ok" };
    case "restarting":
      return { key: "restarting", tone: "warn" };
    case "paused":
      return { key: "paused", tone: "neutral" };
    case "dead":
      return { key: "failing", tone: "danger" };
    case "exited":
    case "removing":
      // Le noyau tue en SIGKILL : 137 comme un docker stop, seul oom_killed
      // les distingue.
      if (container.oom_killed === true) {
        return { key: "out_of_memory", tone: "danger" };
      }
      // docker kill aussi, sans arrêt demandé : l'agent le dit par killed.
      if (container.killed === true) {
        return { key: "killed", tone: "danger" };
      }
      // Un arrêt demandé est propre quel que soit le code (arrêt trop lent).
      return container.stop_requested === true || isCleanExit(container.exit_code) ? { key: "stopped", tone: "neutral" } : { key: "failing", tone: "danger" };
    default:
      return { key: "created", tone: "neutral" };
  }
}

export function containerNeedsAttention(container: PillFacts): boolean {
  const key = displayedState(container).key;
  return key === "failing" || key === "out_of_memory" || key === "killed" || key === "restarting";
}

// Le filtre de la planche « Machine » : Tous, Actifs, Arrêtés.
export type Filter = "all" | "active" | "stopped";

const activeStates: ReadonlySet<ContainerState> = new Set(["running", "restarting"]);

// Un conteneur d'état inconnu n'est ni actif ni arrêté : seul « Tous » le garde.
export function matchesFilter(container: Pick<Container, "state"> & { observed?: boolean }, filter: Filter): boolean {
  if (filter === "all") {
    return true;
  }
  if (container.observed === false) {
    return false;
  }
  return activeStates.has(container.state) === (filter === "active");
}

export function filterContainers(containers: Container[], filter: Filter): Container[] {
  return containers.filter((container) => matchesFilter(container, filter));
}

// Les ports publiés comme la planche les écrit : « :80 · :443 ».
export function formatPublishedPorts(ports: Port[]): string {
  const seen = new Set<number>();
  const parts: string[] = [];
  for (const port of ports) {
    if (!seen.has(port.host_port)) {
      seen.add(port.host_port);
      parts.push(`:${port.host_port}`);
    }
  }
  return parts.join(" · ");
}

// Une liaison telle que l'inspecteur l'écrit : « 0.0.0.0:18081 → 80/tcp ».
export function formatBinding(port: Port): string {
  const host = isPublicIP(port.ip) ? `0.0.0.0:${port.host_port}` : `${port.ip}:${port.host_port}`;
  return `${host} → ${port.container_port}/${port.protocol}`;
}

// Les liaisons uniques : 0.0.0.0 et :: sur le même port n'en font qu'une.
export function formatBindings(ports: Port[]): string[] {
  const lines: string[] = [];
  for (const port of ports) {
    const line = formatBinding(port);
    if (!lines.includes(line)) {
      lines.push(line);
    }
  }
  return lines;
}

export function isPublicIP(ip: string): boolean {
  return ip === "" || ip === "0.0.0.0" || ip === "::";
}

// Le nom de l'image sans registre ni étiquette : « postgres » pour
// « ghcr.io/lib/postgres:16.4 ».
export function imageBase(image: string): string {
  const withoutTag = image.split("@")[0]!.replace(/:[^/]*$/, "");
  return withoutTag.split("/").pop() ?? withoutTag;
}

const databaseImages = /^(postgres|postgresql|mysql|mariadb|redis|valkey|mongo|mongodb|memcached|elasticsearch|opensearch)$/;

// L'icône d'un nœud : une base pour les images de bases de données, le
// carton pour le reste. Le proxy attend sa fonctionnalité.
export function containerIcon(container: Pick<Container, "image">): "box" | "database" {
  return databaseImages.test(imageBase(container.image)) ? "database" : "box";
}

// La clé du catalogue d'un constat, et ce qu'il y a à y insérer.
export function findingText(t: Translate, finding: Finding): string {
  if (finding.port !== undefined) {
    return t(`network.exposure_${finding.kind}`, `${finding.port}/${finding.protocol ?? ""}`);
  }
  return t(`network.exposure_${finding.kind}`);
}

export function needsCaution(container: Pick<Container, "exposure">): boolean {
  return container.exposure.some((finding) => finding.level === "warn");
}

// Le point d'un nœud : le danger d'abord, puis l'attention d'un constat ou
// d'un redémarrage, puis l'état.
export function nodeTone(container: PillFacts & Pick<Container, "exposure">): Tone {
  const state = displayedState(container);
  // Inconnu reste neutre : un constat d'exposition d'avant le silence de la
  // machine n'est pas une alerte d'aujourd'hui.
  if (state.key === "unknown") {
    return state.tone;
  }
  const tone = state.tone;
  if (tone === "danger" || tone === "warn") {
    return tone;
  }
  return needsCaution(container) ? "warn" : tone;
}

// Le sous-titre d'un nœud, en mono : l'image, puis ses ports publiés.
export function nodeSubtitle(container: Pick<Container, "image" | "ports">): string {
  const parts = [imageBase(container.image)];
  const seen = new Set<string>();
  for (const port of container.ports) {
    const label = isPublicIP(port.ip) ? `:${port.host_port}` : `${port.ip}:${port.host_port}`;
    if (!seen.has(label)) {
      seen.add(label);
      parts.push(label);
    }
  }
  return parts.join(" · ");
}

// « Dernier état : Actif, il y a 5 h » sous la pastille « Inconnu » : l'état
// que disaient les faits, et depuis quand la machine s'est tue. Rien quand la
// machine est observée, ou qu'elle n'a jamais signalé.
export function lastStateText(t: Translate, container: PillFacts & Pick<Container, "machine_last_seen_at">, now: number): string | null {
  if (container.observed !== false || container.machine_last_seen_at === null) {
    return null;
  }
  const state = t(`container.state_${containerPill(container).key}`);
  return t("container.last_state", state, formatDuration(t, since(container.machine_last_seen_at, now)));
}

// Le sous-titre de la page Services : rien, un, plusieurs, ce qui demande
// attention, et ce dont l'état est inconnu : « tous actifs » ne se dit pas
// de conteneurs que leur machine ne confirme plus.
export function servicesSubtitle(t: Translate, total: number, attention: number, unknown: number): string {
  if (total === 0) {
    return t("services.subtitle_none");
  }
  if (unknown > 0) {
    if (total === 1) {
      return t("services.subtitle_one_unknown");
    }
    return attention === 0 ? t("services.subtitle_unknown", total, unknown) : t("services.subtitle_attention_unknown", total, attention, unknown);
  }
  if (total === 1) {
    return t(attention === 0 ? "services.subtitle_one" : "services.subtitle_one_attention");
  }
  if (attention === 0) {
    return t("services.subtitle_ok", total);
  }
  return t("services.subtitle_attention", total, attention);
}

// Ce qu'on dit d'une machine sans conteneur : la raison, si elle l'a dite.
export function engineNote(t: Translate, engine: Engine | undefined): string | null {
  if (engine === undefined) {
    return t("services.engine_unknown");
  }
  if (engine.present) {
    return null;
  }
  switch (engine.reason) {
    case "denied":
      return t("services.engine_denied");
    case "down":
      return t("services.engine_down");
    case "too_old":
      return t("services.engine_too_old");
    default:
      return t("services.engine_absent");
  }
}

// Une transition se lit comme un fait : l'action, puis l'état ou la santé
// qu'elle a donnés.
export function transitionTitle(t: Translate, transition: Transition): string {
  if (transition.action === "inventory" && transition.previous_state === "") {
    return t("container.transition_seen");
  }
  if (transition.action === "gone") {
    return t("container.transition_gone");
  }
  // Un état vide : la fiche est archivée, par un destroy ou par l'inventaire.
  if (transition.new_state === "") {
    return transition.action === "destroy" ? t("container.transition_destroyed") : t("container.transition_gone");
  }
  if (transition.action === "health_status" || (transition.new_state === transition.previous_state && transition.new_health !== transition.previous_health)) {
    return t(`container.health_${transition.new_health || "healthy"}`);
  }
  return t(`container.state_${containerPill({ state: transition.new_state, exit_code: transition.exit_code ?? 0, health: transition.new_health, oom_killed: transition.oom_killed, killed: transition.killed, stop_requested: transition.stop_requested }).key}`);
}

// Le passage d'une transition : « Arrêté → Actif », « en bonne santé → en
// mauvaise santé ». Sans état d'avant connu (découverte, disparition,
// destruction) ou sans changement lisible, le titre seul. Le code de sortie
// d'une transition est celui de l'état d'après : l'état d'avant se lit sans.
export function transitionPassage(t: Translate, transition: Transition): string {
  const title = transitionTitle(t, transition);
  if (transition.new_state === "" || (transition.previous_state === "" && transition.previous_health === "")) {
    return title;
  }
  let before = "";
  let after = "";
  if (transition.previous_state !== "" && transition.previous_state !== transition.new_state) {
    before = t(`container.state_${containerPill({ state: transition.previous_state, exit_code: 0, health: transition.previous_health }).key}`);
    after = t(`container.state_${containerPill({ state: transition.new_state, exit_code: transition.exit_code ?? 0, health: transition.new_health, oom_killed: transition.oom_killed, killed: transition.killed, stop_requested: transition.stop_requested }).key}`);
  } else if (transition.previous_health !== "" && transition.previous_health !== transition.new_health) {
    before = t(`container.health_${transition.previous_health}`);
    after = t(`container.health_${transition.new_health || "healthy"}`);
  }
  if (before === "" || before === after) {
    return title;
  }
  return `${before} → ${after}`;
}
