import type { ReactNode } from "react";
import { Link } from "react-router";

import { useT } from "../i18n/context";
import type { StateTone } from "../lib/tone";
import { Meter } from "./Card";
import { Icon, type IconName } from "./Icon";
import { MoreMenu, type MenuAction } from "./MoreMenu";
import "./EntityCard.scss";

// Un fait se lit en mono (une adresse, une version) ; `mono: false` pour
// une phrase (« il y a 12 s »).
interface EntityFact {
  label: string;
  value: ReactNode;
  mono?: boolean;
  // Sur toute la largeur de la carte : une phrase ou une pastille avec son détail.
  wide?: boolean;
}

interface EntityMeter {
  label: string;
  value: ReactNode;
  percent: number;
  tone?: StateTone | undefined;
}

// La grille des cartes d'objet : autant de colonnes que la largeur en tient,
// sans étirer deux cartes sur tout l'écran ; une seule sur un téléphone.
export function CardGrid({ children }: { children: ReactNode }) {
  return <div className="card-grid">{children}</div>;
}

// La carte d'un objet peu nombreux et riche en état (une machine, un service
// déclaré, une page de statut) : nom, type, pastille, deux à quatre faits en
// mono, des mini-jauges au besoin. Toute la carte mène à la fiche : le lien du
// nom s'étire sur elle, ce qui garde un vrai lien (Ctrl-clic, clavier) sans
// mettre le bouton « … » dans un lien. Sans `to`, un objet qui n'a pas de fiche
// (une VM que nulle machine enrôlée ne porte) : le nom est du texte, la carte
// n'est pas cliquable. `children` : ce qui suit les jauges (la confirmation
// d'un geste, son avancement).
export function EntityCard({
  to,
  icon,
  name,
  kind,
  state,
  facts,
  meters = [],
  actions = [],
  children,
}: {
  to?: string;
  icon: IconName;
  name: string;
  kind: string;
  state?: ReactNode;
  facts: EntityFact[];
  meters?: EntityMeter[];
  actions?: MenuAction[];
  children?: ReactNode;
}) {
  const t = useT();
  return (
    <article className="card entity-card">
      <div className="entity-head">
        <span className="entity-icon">
          <Icon name={icon} />
        </span>
        {to === undefined ? (
          <span className="entity-link">{name}</span>
        ) : (
          <Link className="entity-link linked" to={to}>
            {name}
          </Link>
        )}
        <span className="entity-sub">
          <span className="entity-kind">{kind}</span>
          {state}
        </span>
        {actions.length > 0 && <MoreMenu actions={actions} label={t("entity.more", name)} />}
      </div>
      <dl className="entity-facts">
        {facts.map((fact) => (
          <div key={fact.label} className={fact.wide === true ? "wide" : undefined}>
            <dt>{fact.label}</dt>
            <dd className={fact.mono === false ? undefined : "mono"}>{fact.value}</dd>
          </div>
        ))}
      </dl>
      {meters.length > 0 && (
        <div className="entity-meters">
          {meters.map((meter) => (
            <div className="entity-meter" key={meter.label}>
              <span className="entity-meter-label">
                <span>{meter.label}</span>
                <span className="mono">{meter.value}</span>
              </span>
              <Meter percent={meter.percent} tone={meter.tone} />
            </div>
          ))}
        </div>
      )}
      {children !== undefined && <div className="entity-extra">{children}</div>}
    </article>
  );
}
