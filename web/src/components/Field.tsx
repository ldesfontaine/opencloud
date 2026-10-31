import type { InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from "react";

import { FieldTip } from "./FieldTip";
import "./Field.scss";

// Un champ : étiquette, contrôle, aide. L'aide tient sur une ligne ; le
// détail, quand il sert vraiment, va dans tip, une info-bulle « ? » à côté
// de l'étiquette.
export function Field({
  label,
  htmlFor,
  help,
  tip,
  children,
}: {
  label: string;
  htmlFor?: string;
  help?: string;
  tip?: string;
  children: ReactNode;
}) {
  const caption = htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <span className="field-label">{label}</span>;
  return (
    <div className="field">
      {tip === undefined ? (
        caption
      ) : (
        <span className="field-caption">
          {caption}
          <FieldTip text={tip} />
        </span>
      )}
      {children}
      {help !== undefined && <span className="field-help">{help}</span>}
    </div>
  );
}

export function Input({ mono = false, className, ...rest }: InputHTMLAttributes<HTMLInputElement> & { mono?: boolean }) {
  const classes = ["input"];
  if (mono) {
    classes.push("mono");
  }
  if (className) {
    classes.push(className);
  }
  return <input className={classes.join(" ")} {...rest} />;
}

export function Textarea({ mono = false, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement> & { mono?: boolean }) {
  return <textarea className={mono ? "input textarea mono" : "input textarea"} {...rest} />;
}

// Des cases à cocher côte à côte, chacune avec son libellé.
export function Checks({ children }: { children: ReactNode }) {
  return <div className="checks">{children}</div>;
}

// disabled : une case montrée pour ce qu'elle dit, qu'on ne change pas.
export function Checkbox({ label, checked, onChange, disabled = false }: { label: string; checked: boolean; onChange: (checked: boolean) => void; disabled?: boolean }) {
  return (
    <label>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(event) => onChange(event.target.checked)} />
      {label}
    </label>
  );
}
