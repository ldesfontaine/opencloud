import type { ReactNode } from "react";

import { Icon } from "./Icon";
import "./ExternalLink.scss";

// Un lien vers un autre site (la page d'un pare-feu, d'un hôte) : il s'ouvre
// à côté pour que la saisie en cours reste là, et sans transmettre la page
// d'origine.
export function ExternalLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a className="external-link" href={href} target="_blank" rel="noopener noreferrer">
      {children}
      <Icon name="external" />
    </a>
  );
}
