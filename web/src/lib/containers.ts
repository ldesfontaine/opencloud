import type { Engine, Finding, Port, Container, ContainerState, Transition } from "../api/types";
import type { Tone } from "./tone";
import type { Translate } from "../i18n/context";

// Les pastilles de la direction artistique, dérivées des faits : l'état
// Docker, le code de sortie, la santé. La même règle que le serveur pour
// « demande attention ».
export type PillKey = "active" | "starting" | "failing" | "out_of_memory" | "killed" | "restarting" | "paused" | "stopped" | "created";

export interface ContainerPillInfo {
  key: PillKey;
  tone: Tone;
}

// 0 est une fin normale, 137 et 143 un arrêt demandé (docker stop).
export function isCleanExit(code: number): boolean {
  return code === 0 || code === 137 || code === 143;
}

// Les faits dont la pastille dépend ; oom_killed et killed manquent sur
// une fiche d'avant leur correctif.
export type PillFacts = Pick<Container, "state" | "exit_code" | "health"> & { oom_killed?: boolean; killed?: boolean };

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
      return isCleanExit(container.exit_code) ? { key: "stopped", tone: "neutral" } : { key: "failing", tone: "danger" };
    default:
      return { key: "created", tone: "neutral" };
  }
}

export function containerNeedsAttention(container: PillFacts): boolean {
  const key = containerPill(container).key;
  return key === "failing" || key === "out_of_memory" || key === "killed" || key === "restarting";
}

// Le filtre de la planche « Machine » : Tous, Actifs, Arrêtés.
export type Filter = "all" | "active" | "stopped";

const activeStates: ReadonlySet<ContainerState> = new Set(["running", "restarting"]);

export function matchesFilter(container: Pick<Container, "state">, filter: Filter): boolean {
  if (filter === "all") {
    return true;
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
  const tone = containerPill(container).tone;
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

// Le sous-titre de la page Services : rien, un, plusieurs, et ce qui
// demande attention.
export function servicesSubtitle(t: Translate, total: number, attention: number): string {
  if (total === 0) {
    return t("services.subtitle_none");
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
  return t(`container.state_${containerPill({ state: transition.new_state, exit_code: transition.exit_code ?? 0, health: transition.new_health, oom_killed: transition.oom_killed, killed: transition.killed }).key}`);
}
