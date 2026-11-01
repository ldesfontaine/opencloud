import type { ReactNode } from "react";

import { Checkbox } from "./Field";
import "./Checklist.scss";

// Un geste à faire ailleurs : son titre (la case), et ce qu'il faut savoir
// pour le faire.
export interface ChecklistItem {
  id: string;
  title: string;
  content: ReactNode;
}

// Des gestes à cocher au fil de l'eau. Les cases aident à suivre où l'on en
// est ; rien ne les exige. Ce qui est coché vit chez l'appelant, pour qu'une
// étape quittée n'oublie rien.
export function Checklist({ items, done, onToggle }: { items: ChecklistItem[]; done: readonly string[]; onToggle: (id: string, checked: boolean) => void }) {
  return (
    <ol className="checklist">
      {items.map((item) => (
        <li key={item.id} className="checklist-item">
          <Checkbox label={item.title} checked={done.includes(item.id)} onChange={(checked) => onToggle(item.id, checked)} />
          <div className="checklist-body">{item.content}</div>
        </li>
      ))}
    </ol>
  );
}
