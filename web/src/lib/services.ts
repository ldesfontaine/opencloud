import type { Engine, Port, Service, ServiceState, Transition } from "../api/types";
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
