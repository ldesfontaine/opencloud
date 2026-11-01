import type { ReactNode } from "react";

import { Rich } from "./Rich";
import "./SectionHead.scss";

// Le titre d'une section de page : l'étiquette en capitales, une phrase qui
// dit ce qu'on y règle, et à droite l'état de la section quand elle en a un.
// id : celui du titre, que la section désigne par aria-labelledby.
export function SectionHead({ id, title, text, aside }: { id: string; title: string; text: string; aside?: ReactNode }) {
  return (
    <header className="section-head">
      <div className="section-head-text">
        <h2 className="side-label section-label" id={id}>
          {title}
        </h2>
        <p>
          <Rich text={text} />
        </p>
      </div>
      {aside !== undefined && <div className="section-head-aside">{aside}</div>}
    </header>
  );
}
