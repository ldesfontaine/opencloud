import type { ButtonHTMLAttributes, ReactNode } from "react";
import { Link } from "react-router";

import { Icon, type IconName } from "./Icon";
import "./Button.scss";

type Variant = "primary" | "secondary" | "ghost" | "danger" | "accent";

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  small?: boolean;
  icon?: IconName;
  // Avec `to`, le bouton est un lien : même dessin, navigation du routeur.
  to?: string;
  // Avec `href`, c'est un lien vers un fichier que le serveur sert : le
  // navigateur le télécharge, avec les identifiants qu'il a déjà pour l'interface.
  href?: string;
  download?: string;
  children?: ReactNode;
}

function classes(variant: Variant, small: boolean, iconOnly: boolean): string {
  const list = ["btn", `btn-${variant}`];
  if (small) {
    list.push("btn-sm");
  }
  if (iconOnly) {
    list.push("btn-icon");
  }
  return list.join(" ");
}

export function Button({ variant = "secondary", small = false, icon, to, href, download, children, type = "button", ...rest }: Props) {
  const className = classes(variant, small, icon !== undefined && children === undefined);
  const content = (
    <>
      {icon && <Icon name={icon} />}
      {children}
    </>
  );
  if (to !== undefined) {
    return (
      <Link className={className} to={to}>
        {content}
      </Link>
    );
  }
  if (href !== undefined) {
    return (
      <a className={className} href={href} download={download} title={rest.title} aria-label={rest["aria-label"]}>
        {content}
      </a>
    );
  }
  return (
    <button className={className} type={type} {...rest}>
      {content}
    </button>
  );
}
