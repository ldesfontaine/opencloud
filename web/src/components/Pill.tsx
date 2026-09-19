import type { ReactNode } from "react";

import type { JobStatus, Service } from "../api/types";
import { useT } from "../i18n/context";
import { servicePill } from "../lib/services";
import "./Pill.scss";

export type Tone = "ok" | "warn" | "danger" | "neutral" | "accent";

// Un état se lit par une pastille : fond lavis, texte, point. Jamais par du
// texte coloré seul.
export function Pill({ tone, dot = true, children }: { tone: Tone; dot?: boolean; children: ReactNode }) {
  return <span className={`pill pill-${tone}${dot ? "" : " nodot"}`}>{children}</span>;
}

export function Dot({ tone }: { tone: Tone }) {
  return <span className={`dot ${tone}`} />;
}

export function StatePill({ online }: { online: boolean }) {
  const t = useT();
  return <Pill tone={online ? "ok" : "danger"}>{t(online ? "state.online" : "state.offline")}</Pill>;
}

// Pastille par état de tâche : le vocabulaire de la direction artistique.
const jobTones: Record<JobStatus, Tone> = {
  new: "neutral",
  on_time: "ok",
  started: "accent",
  late: "danger",
  failed: "danger",
  paused: "neutral",
};

export function JobPill({ status }: { status: JobStatus }) {
  const t = useT();
  return <Pill tone={jobTones[status]}>{t(`job.status_${status}`)}</Pill>;
}

// Pastille d'un service : Actif, Démarre, Défaillant, Redémarre, En pause,
// Arrêté ; dérivée des faits, jamais envoyée par le serveur.
export function ServicePill({ service }: { service: Pick<Service, "state" | "exit_code" | "health"> }) {
  const t = useT();
  const pill = servicePill(service);
  return <Pill tone={pill.tone}>{t(`service.state_${pill.key}`)}</Pill>;
}
