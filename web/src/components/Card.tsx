import type { ReactNode } from "react";

import { Icon, type IconName } from "./Icon";
import "./Card.scss";

// Les cartes se distinguent par un trait, jamais par une ombre.
export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <section className={className ? `card ${className}` : "card"}>{children}</section>;
}

export function CardHeader({ title, aside }: { title: string; aside?: ReactNode }) {
  return (
    <div className="card-h">
      <span className="card-t">{title}</span>
      {aside !== undefined && <span className="card-s">{aside}</span>}
    </div>
  );
}

export function CardBody({ gap, children }: { gap?: 8 | 10 | 12 | 16 | 24; children: ReactNode }) {
  return <div className={gap ? `card-b gap-${gap}` : "card-b"}>{children}</div>;
}

export function KeyValue({ label, value, mono = false }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <div className="kv">
      <span className="k">{label}</span>
      <span className={mono ? "mono" : undefined}>{value}</span>
    </div>
  );
}

// Chiffre clé : étiquette avec icône, valeur, jauge optionnelle, une ligne
// de contexte.
export function Stat({
  icon,
  label,
  value,
  unit,
  meter,
  foot,
}: {
  icon: IconName;
  label: string;
  value: ReactNode;
  unit?: string;
  meter?: ReactNode;
  foot: ReactNode;
}) {
  return (
    <Card>
      <div className="card-b stat">
        <span className="label">
          <Icon name={icon} />
          {label}
        </span>
        <span className="stat-value">
          {value}
          {unit !== undefined && <small>{unit}</small>}
        </span>
        {meter}
        <span className="foot">{foot}</span>
      </div>
    </Card>
  );
}

// Jauge : le remplissage est un attribut SVG, pas un style en ligne, pour
// rester dans la CSP.
export function Meter({ percent, tone }: { percent: number; tone?: "ok" | "warn" | "danger" }) {
  return (
    <svg className={tone ? `meter meter-${tone}` : "meter"} width="100%" height="6" aria-hidden="true">
      <rect className="meter-track" width="100%" height="6" rx="3" />
      <rect className="meter-fill" width={`${percent}%`} height="6" rx="3" />
    </svg>
  );
}

export function Empty({
  icon,
  title,
  text,
  action,
}: {
  icon?: IconName;
  title?: string;
  text: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty">
      {icon && (
        <span className="empty-icon">
          <Icon name={icon} />
        </span>
      )}
      {title !== undefined && <h2>{title}</h2>}
      <p>{text}</p>
      {action}
    </div>
  );
}

export function Note({ tone, children }: { tone: "warn" | "danger"; children: ReactNode }) {
  return <div className={`note note-${tone}`}>{children}</div>;
}
