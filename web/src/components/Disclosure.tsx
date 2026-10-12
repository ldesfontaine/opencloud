import { type ReactNode, useState } from "react";

import { Card } from "./Card";
import { Icon } from "./Icon";
import "./Disclosure.scss";

// Ce qui a servi une fois (une installation, un réglage) reste à portée
// sans occuper l'écran. L'ouverture se décide au montage ; ensuite c'est
// l'opérateur qui ouvre et ferme, un rafraîchissement n'y touche pas.
function useFold(initiallyOpen: boolean) {
  const [isOpen, setIsOpen] = useState(initiallyOpen);
  return { isOpen, follow: (details: HTMLDetailsElement) => setIsOpen(details.open) };
}

// Une carte entière qui se replie sous son en-tête.
export function Disclosure({
  title,
  aside,
  initiallyOpen,
  children,
}: {
  title: string;
  aside?: ReactNode;
  initiallyOpen: boolean;
  children: ReactNode;
}) {
  const fold = useFold(initiallyOpen);
  return (
    <Card className="disclosure">
      <details open={fold.isOpen} onToggle={(event) => fold.follow(event.currentTarget)}>
        <summary className="card-h">
          <span className="card-t">{title}</span>
          <span className="fold-end">
            {aside !== undefined && <span className="card-s">{aside}</span>}
            <span className="fold-chevron">
              <Icon name="chevron-down" />
            </span>
          </span>
        </summary>
        {children}
      </details>
    </Card>
  );
}

// Un pli à l'intérieur d'une carte : le détail qu'on ne veut pas à l'écran.
export function Fold({ label, initiallyOpen, children }: { label: string; initiallyOpen: boolean; children: ReactNode }) {
  const fold = useFold(initiallyOpen);
  return (
    <details className="fold" open={fold.isOpen} onToggle={(event) => fold.follow(event.currentTarget)}>
      <summary>
        <span className="fold-chevron">
          <Icon name="chevron-down" />
        </span>
        {label}
      </summary>
      <div className="fold-body">{children}</div>
    </details>
  );
}

// Une partie d'une carte repliée : son titre, sa phrase, son contenu.
export function FoldSection({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return (
    <div className="fold-section">
      <div className="fold-section-head">
        <span className="card-t">{title}</span>
        <span className="secondary">{text}</span>
      </div>
      {children}
    </div>
  );
}
