import type { MouseEvent, ReactNode } from "react";
import { Link } from "react-router";

import { useT } from "../i18n/context";
import { rowClickAction } from "../lib/rowLink";
import { Icon, type IconName } from "./Icon";
import { MoreMenu, type MenuAction } from "./MoreMenu";
import "./Table.scss";

// Tableau : en-têtes en étiquette de section, lignes de 44 px, valeurs en
// mono. Il défile dans son propre cadre : trop large pour un téléphone, il
// ne pousse jamais la page.
// `fit` : chaque colonne prend la largeur de son contenu, la première (le
// nom) prend le reste ; le vide ne s'étale plus entre les colonnes.
export function Table({ fit = false, children }: { fit?: boolean; children: ReactNode }) {
  return (
    <div className="table-scroll">
      <table className={fit ? "table table-fit" : "table"} onClick={followRowLink} onAuxClick={followRowLink}>
        {children}
      </table>
    </div>
  );
}

// Ce qui répond seul à un clic dans une ligne cliquable.
const rowControls = "a, button, input, select, textarea, label, summary, [role=menu]";

// Une ligne qui porte un RowLink suit son lien, où qu'on clique. Le clic
// s'écoute sur le tableau, pas en étirant le lien : un <tr> positionné ne
// tient pas dans tous les navigateurs. Les lignes sans RowLink ne changent pas.
function followRowLink(event: MouseEvent<HTMLTableElement>) {
  const target = event.target;
  if (!(target instanceof Element)) {
    return;
  }
  const row = target.closest("tr");
  const link = row?.querySelector<HTMLAnchorElement>("a.row-link");
  if (!row || !link) {
    return;
  }
  const control = target.closest(rowControls);
  const selection = window.getSelection();
  const action = rowClickAction({
    button: event.button,
    withModifier: event.ctrlKey || event.metaKey || event.shiftKey,
    onControl: control !== null && row.contains(control),
    selectingText: selection !== null && !selection.isCollapsed && row.contains(selection.anchorNode),
  });
  if (action === "follow") {
    link.click();
  }
  if (action === "new-tab") {
    window.open(link.href, "_blank", "noopener");
  }
}

// Le nom d'une ligne qui mène à la fiche : la ligne entière devient
// cliquable, sans bouton « Ouvrir ». Le nom reste un vrai lien : le clavier
// et le Ctrl-clic y marchent. Les actions secondaires suivent le nom, dans
// le menu « … ».
export function RowLink({ to, icon, actions = [], children }: { to: string; icon: IconName; actions?: MenuAction[]; children: string }) {
  const t = useT();
  return (
    <span className="row-name">
      <Link className="row-link" to={to}>
        <Subject icon={icon}>{children}</Subject>
      </Link>
      {actions.length > 0 && <MoreMenu actions={actions} label={t("entity.more", children)} />}
    </span>
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
