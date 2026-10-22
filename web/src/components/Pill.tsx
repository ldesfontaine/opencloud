import type { ReactNode } from "react";

import type { JobStatus } from "../api/jobs";
import type { ProbeStatus } from "../api/probes";
import { useT } from "../i18n/context";
import { displayedState, type PillFacts } from "../lib/containers";
import { jobTones } from "../lib/jobs";
import { probeTones } from "../lib/probes";
import type { Tone } from "../lib/tone";
import "./Pill.scss";

// Un état se lit par une pastille : fond lavis, texte, point. Jamais par du
// texte coloré seul.
// `small` tient dans une ligne de texte de 12 px (le pied d'une tuile, le titre
// d'une ligne de liste) sans la faire grandir.
// Dans un pied de tuile étroit, son texte se tronque (…) plutôt que de couper
// la pastille ; `title` dit alors le texte entier.
export function Pill({ tone, dot = true, small = false, title, children }: { tone: Tone; dot?: boolean; small?: boolean; title?: string; children: ReactNode }) {
  return (
    <span className={`pill pill-${tone}${dot ? "" : " nodot"}${small ? " pill-sm" : ""}`} title={title}>
      <span className="pill-label">{children}</span>
    </span>
  );
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
// serveur. Inconnu, en neutre, quand la machine ne signale plus.
export function ContainerPill({ container }: { container: PillFacts }) {
  const t = useT();
  const pill = displayedState(container);
  return <Pill tone={pill.tone}>{t(`container.state_${pill.key}`)}</Pill>;
}

// Pastille d'une sonde : Nouveau, En ligne, Dégradé, Hors ligne, En pause.
export function ProbePill({ status }: { status: ProbeStatus }) {
  const t = useT();
  return <Pill tone={probeTones[status]}>{t(`probe.status_${status}`)}</Pill>;
}
