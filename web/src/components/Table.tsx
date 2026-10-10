import type { ReactNode } from "react";

import { Icon, type IconName } from "./Icon";
import "./Table.scss";

// Tableau : en-têtes en étiquette de section, lignes de 44 px, valeurs en
// mono, action en fin de ligne. Il défile dans son propre cadre : trop
// large pour un téléphone, il ne pousse jamais la page.
export function Table({ children }: { children: ReactNode }) {
  return (
    <div className="table-scroll">
      <table className="table">{children}</table>
    </div>
  );
}

// Un nom avec son icône dans une tuile, en tête de ligne.
export function Subject({ icon, children }: { icon: IconName; children: ReactNode }) {
  return (
    <span className="svc">
      <span className="ico">
        <Icon name={icon} />
      </span>
      {children}
    </span>
  );
}

// Liste d'événements : point d'état, fait en gras léger, contexte en
// dessous, moment à droite.
export function List({ children }: { children: ReactNode }) {
  return <div className="list">{children}</div>;
}

export function RowText({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <span className="txt">
      <b>{title}</b>
      {children !== undefined && <span>{children}</span>}
    </span>
  );
}

export function RowWhen({ children }: { children: ReactNode }) {
  return <span className="when">{children}</span>;
}
