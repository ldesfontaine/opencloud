import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from "react";

import { useT } from "../i18n/context";
import { Icon, type IconName } from "./Icon";
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

export function Select({ icon, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement> & { icon?: IconName }) {
  return (
    <span className="select">
      {icon && <Icon name={icon} />}
      <select {...rest}>{children}</select>
      <span className="chev">
        <Icon name="chevron-down" />
      </span>
    </span>
  );
}

// Un choix dans une liste fermée : chaque option se traduit par la clé
// labelKey + valeur (channel.format_ + slack), et la valeur choisie se
// retrouve dans values plutôt que d'être forcée au type.
export function EnumSelect<Value extends string>({
  value,
  values,
  labelKey,
  onChange,
  ...rest
}: Omit<SelectHTMLAttributes<HTMLSelectElement>, "value" | "onChange" | "children"> & {
  value: Value;
  values: readonly Value[];
  labelKey: string;
  onChange: (value: Value) => void;
  icon?: IconName;
}) {
  const t = useT();
  const choose = (chosen: string) => {
    const known = values.find((candidate) => candidate === chosen);
    if (known !== undefined) {
      onChange(known);
    }
  };
  return (
    <Select value={value} onChange={(event) => choose(event.target.value)} {...rest}>
      {values.map((candidate) => (
        <option key={candidate} value={candidate}>
          {t(`${labelKey}${candidate}`)}
        </option>
      ))}
    </Select>
  );
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
