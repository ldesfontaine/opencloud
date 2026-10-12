import type { ReactNode } from "react";

import type { Tone } from "./Pill";
import "./Verdict.scss";

// Le verdict d'une page : la pastille d'état, le fait en une phrase, ce
// qui le précise, puis les métadonnées. C'est la réponse à « est-ce que ça
// va ? », avant tout le reste. Un état qui demande l'opérateur teinte la
// carte ; les autres la laissent blanche.
export function Verdict({
  tone,
  pill,
  fact,
  context,
  children,
}: {
  tone: Tone;
  pill: ReactNode;
  fact: string;
  context?: string | null;
  children?: ReactNode;
}) {
  return (
    <section className={`card verdict verdict-${tone}`}>
      <div className="verdict-head">
        {pill}
        <b className="verdict-fact">{fact}</b>
      </div>
      {context !== undefined && context !== null && <span className="verdict-context">{context}</span>}
      {children !== undefined && <span className="meta">{children}</span>}
    </section>
  );
}
