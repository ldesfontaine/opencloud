import type { ReactNode } from "react";

import { useT } from "../i18n/context";
import "./SettingRow.scss";

// Une ligne de réglage : l'étiquette et sa description à gauche, le contrôle
// à droite (dessous sur un téléphone). width : la place du contrôle, à la
// mesure de sa valeur (un pourcentage tient dans 6 caractères). block : le
// contrôle prend toute la largeur sous le texte, pour une note ou un bloc.
export type SettingWidth = "short" | "medium" | "wide";

export function SettingRow({
  label,
  description,
  htmlFor,
  width,
  block = false,
  children,
}: {
  label: string;
  description?: string | undefined;
  htmlFor?: string;
  width?: SettingWidth;
  block?: boolean;
  children?: ReactNode;
}) {
  const classes = ["setting-row"];
  if (width !== undefined) {
    classes.push(`setting-${width}`);
  }
  if (block) {
    classes.push("setting-block-row");
  }
  return (
    <div className={classes.join(" ")}>
      <div className="setting-text">
        {htmlFor === undefined ? <span className="setting-label">{label}</span> : <label className="setting-label" htmlFor={htmlFor}>{label}</label>}
        {description !== undefined && description !== "" && <span className="setting-description">{description}</span>}
      </div>
      {children !== undefined && <div className="setting-control">{children}</div>}
    </div>
  );
}

// Un bloc libre dans une carte de réglages : une note, une erreur, un
// tableau n'ont pas d'étiquette à gauche.
export function SettingBlock({ children }: { children: ReactNode }) {
  return <div className="setting-block">{children}</div>;
}

// Le pied d'une carte de réglages : ce qui se dit en passant à gauche (une
// pastille « Enregistré », la portée du réglage), l'unique bouton principal
// de la section à droite.
export function SettingFoot({ note, children }: { note?: ReactNode; children?: ReactNode }) {
  return (
    <div className="setting-foot">
      <div className="setting-foot-note">{note}</div>
      {children !== undefined && <div className="setting-foot-actions">{children}</div>}
    </div>
  );
}

// Ce qui détruit ou ferme : à part, sous un trait, en pied de la section.
export function DangerZone({ children }: { children: ReactNode }) {
  const t = useT();
  return (
    <div className="setting-danger">
      <span className="setting-danger-title">{t("settings.danger_zone")}</span>
      {children}
    </div>
  );
}
