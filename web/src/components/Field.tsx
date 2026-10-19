import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from "react";

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
