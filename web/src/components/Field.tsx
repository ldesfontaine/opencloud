import type { InputHTMLAttributes, ReactNode, TextareaHTMLAttributes } from "react";

import "./Field.scss";

// Un champ : étiquette, contrôle, aide.
export function Field({
  label,
  htmlFor,
  help,
  children,
}: {
  label: string;
  htmlFor?: string;
  help?: string;
  children: ReactNode;
}) {
  return (
    <div className="field">
      {htmlFor ? (
        <label htmlFor={htmlFor}>{label}</label>
      ) : (
        <span className="field-label">{label}</span>
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
