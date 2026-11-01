import { type ReactNode, useEffect, useRef, useState } from "react";

import { useT } from "../i18n/context";

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
// hint : ce qu'il contient, d'un mot, lisible pli fermé. Un champ invalide
// dans un pli fermé ne peut pas être montré par le navigateur (il bloque
// l'envoi sans rien dire) : le pli s'ouvre dessus.
export function Fold({ label, hint, initiallyOpen, children }: { label: string; hint?: string; initiallyOpen: boolean; children: ReactNode }) {
  const fold = useFold(initiallyOpen);
  const details = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const element = details.current;
    if (element === null) {
      return;
    }
    const openOnInvalid = () => {
      element.open = true;
    };
    // L'événement invalid ne remonte pas : seule la capture le voit d'en haut.
    element.addEventListener("invalid", openOnInvalid, true);
    return () => element.removeEventListener("invalid", openOnInvalid, true);
  }, []);
  return (
    <details ref={details} className="fold" open={fold.isOpen} onToggle={(event) => fold.follow(event.currentTarget)}>
      <summary>
        <span className="fold-chevron">
          <Icon name="chevron-down" />
        </span>
        {label}
        {hint !== undefined && <span className="fold-hint">{hint}</span>}
      </summary>
      <div className="fold-body">{children}</div>
    </details>
  );
}

// Les réglages dont les valeurs par défaut suffisent : repliés sous « Réglages »,
// avec ces valeurs dites en clair (hint), pour qu'on sache ce qu'on accepte
// sans l'ouvrir. Le patron de tout formulaire court (voir Wizard).
export function SettingsFold({ hint, initiallyOpen = false, children }: { hint: string; initiallyOpen?: boolean; children: ReactNode }) {
  const t = useT();
  return (
    <Fold label={t("wizard.settings")} hint={hint} initiallyOpen={initiallyOpen}>
      {children}
    </Fold>
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
