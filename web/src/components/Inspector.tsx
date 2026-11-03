import { type ReactNode } from "react";

import { useT } from "../i18n/context";
import { Button } from "./Button";
import { Icon, type IconName } from "./Icon";
import "./Inspector.scss";

// L'inspecteur : le panneau à droite d'une vue (340 px) qui détaille l'objet
// choisi sans quitter la page : son verdict, ses faits, ses gestes, puis la
// zone de danger. Il ne montre rien que la fiche de l'objet ne montre aussi.
export function Inspector({
  icon,
  title,
  subtitle,
  verdict,
  onClose,
  actions,
  danger,
  children,
}: {
  icon: IconName;
  title: string;
  subtitle: string;
  // La pastille du verdict, déjà dessinée.
  verdict: ReactNode;
  onClose: () => void;
  // Les gestes, un par ligne.
  actions: ReactNode;
  danger?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <aside className="card inspector" aria-label={t("inspector.label", title)}>
      <header className="inspector-head">
        <span className="inspector-badge" aria-hidden="true">
          <Icon name={icon} />
        </span>
        <div className="inspector-title">
          <h3>{title}</h3>
          <span className="secondary">{subtitle}</span>
        </div>
        <Button small variant="ghost" icon="x" aria-label={t("inspector.close")} onClick={onClose} />
      </header>
      <div className="inspector-body">
        <div>{verdict}</div>
        {children}
        <div className="inspector-actions">{actions}</div>
      </div>
      {danger !== undefined && <div className="inspector-danger">{danger}</div>}
    </aside>
  );
}
