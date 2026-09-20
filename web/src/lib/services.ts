import type { Engine, Finding, Port, Service, ServiceState, Transition } from "../api/types";
import type { Tone } from "../components/Pill";
import type { Translate } from "../i18n/context";

// Les pastilles de la direction artistique, dérivées des faits : l'état
// Docker, le code de sortie, la santé. La même règle que le serveur pour
// « demande attention ».
export type PillKey = "active" | "starting" | "failing" | "restarting" | "paused" | "stopped" | "created";

export interface ServicePillInfo {
  key: PillKey;
  tone: Tone;
}

// 0 est une fin normale, 137 et 143 un arrêt demandé (docker stop).
export function isCleanExit(code: number): boolean {
  return code === 0 || code === 137 || code === 143;
}

export function servicePill(service: Pick<Service, "state" | "exit_code" | "health">): ServicePillInfo {
  switch (service.state) {
    case "running":
      if (service.health === "unhealthy") {
        return { key: "failing", tone: "danger" };
      }
      if (service.health === "starting") {
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
      return isCleanExit(service.exit_code) ? { key: "stopped", tone: "neutral" } : { key: "failing", tone: "danger" };
    default:
      return { key: "created", tone: "neutral" };
  }
}

export function needsAttention(service: Pick<Service, "state" | "exit_code" | "health">): boolean {
  const key = servicePill(service).key;
  return key === "failing" || key === "restarting";
}

// Le filtre de la planche « Machine » : Tous, Actifs, Arrêtés.
export type Filter = "all" | "active" | "stopped";

const activeStates: ReadonlySet<ServiceState> = new Set(["running", "restarting"]);

export function matchesFilter(service: Pick<Service, "state">, filter: Filter): boolean {
  if (filter === "all") {
    return true;
  }
  return activeStates.has(service.state) === (filter === "active");
}

// Les ports publiés comme la planche les écrit : « :80 · :443 ».
export function formatPorts(ports: Port[]): string {
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
export function serviceIcon(service: Pick<Service, "image">): "box" | "database" {
  return databaseImages.test(imageBase(service.image)) ? "database" : "box";
}

// La clé du catalogue d'un constat, et ce qu'il y a à y insérer.
export function findingText(t: Translate, finding: Finding): string {
  if (finding.port !== undefined) {
    return t(`network.exposure_${finding.kind}`, `${finding.port}/${finding.protocol ?? ""}`);
  }
  return t(`network.exposure_${finding.kind}`);
}

export function needsCaution(service: Pick<Service, "exposure">): boolean {
  return service.exposure.some((finding) => finding.level === "warn");
}

// Le point d'un nœud : le danger d'abord, puis l'attention d'un constat ou
// d'un redémarrage, puis l'état.
export function nodeTone(service: Pick<Service, "state" | "exit_code" | "health" | "exposure">): Tone {
  const tone = servicePill(service).tone;
  if (tone === "danger" || tone === "warn") {
    return tone;
  }
  return needsCaution(service) ? "warn" : tone;
}

// Le sous-titre d'un nœud, en mono : l'image, puis ses ports publiés.
export function nodeSubtitle(service: Pick<Service, "image" | "ports">): string {
  const parts = [imageBase(service.image)];
  const seen = new Set<string>();
  for (const port of service.ports) {
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

// Ce qu'on dit d'une machine sans service : la raison, si elle l'a dite.
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
    return t("service.transition_seen");
  }
  if (transition.action === "gone") {
    return t("service.transition_gone");
  }
  if (transition.new_state === "" ) {
    return transition.action;
  }
  if (transition.action === "health_status" || (transition.new_state === transition.previous_state && transition.new_health !== transition.previous_health)) {
    return t(`service.health_${transition.new_health || "healthy"}`);
  }
  return t(`service.state_${servicePill({ state: transition.new_state, exit_code: transition.exit_code ?? 0, health: transition.new_health }).key}`);
}
