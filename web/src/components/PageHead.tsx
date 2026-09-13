import { useEffect, type ReactNode } from "react";

import { useT } from "../i18n/context";

// En-tête de page : titre, sous-titre d'une phrase, actions à droite. Une
// seule action principale par écran. Le titre de l'onglet suit.
export function PageHead({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  const t = useT();
  useEffect(() => {
    document.title = `${title} · ${t("app.name")}`;
  }, [title, t]);
  return (
    <header className="page-head">
      <div>
        <h1>{title}</h1>
        {subtitle !== undefined && subtitle !== "" && <p className="sub">{subtitle}</p>}
      </div>
      <div className="actions">{actions}</div>
    </header>
  );
}
