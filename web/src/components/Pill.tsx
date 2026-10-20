import type { ReactNode } from "react";

import type { JobStatus } from "../api/jobs";
import type { ProbeStatus } from "../api/probes";
import { useT } from "../i18n/context";
import { containerPill, type PillFacts } from "../lib/containers";
import { jobTones } from "../lib/jobs";
import { probeTones } from "../lib/probes";
import type { Tone } from "../lib/tone";
import "./Pill.scss";

// Un état se lit par une pastille : fond lavis, texte, point. Jamais par du
// texte coloré seul.
export function Pill({ tone, dot = true, children }: { tone: Tone; dot?: boolean; children: ReactNode }) {
  return <span className={`pill pill-${tone}${dot ? "" : " nodot"}`}>{children}</span>;
}

export function Dot({ tone }: { tone: Tone }) {
  return <span className={`dot ${tone}`} />;
}

export function OnlinePill({ online }: { online: boolean }) {
  const t = useT();
  return <Pill tone={online ? "ok" : "danger"}>{t(online ? "state.online" : "state.offline")}</Pill>;
}

export function JobPill({ status }: { status: JobStatus }) {
  const t = useT();
  return <Pill tone={jobTones[status]}>{t(`job.status_${status}`)}</Pill>;
}

// Pastille d'un conteneur : Actif, Démarre, Défaillant, Manque de mémoire,
// Redémarre, En pause, Arrêté ; dérivée des faits, jamais envoyée par le
// serveur.
export function ContainerPill({ container }: { container: PillFacts }) {
  const t = useT();
  const pill = containerPill(container);
  return <Pill tone={pill.tone}>{t(`container.state_${pill.key}`)}</Pill>;
}

// Pastille d'une sonde : Nouveau, En ligne, Dégradé, Hors ligne, En pause.
export function ProbePill({ status }: { status: ProbeStatus }) {
  const t = useT();
  return <Pill tone={probeTones[status]}>{t(`probe.status_${status}`)}</Pill>;
}
