import type { ReactNode } from "react";

import "./FormPage.scss";

// La colonne d'une page de formulaire : l'en-tête et les cartes ensemble,
// bornés et centrés, comme Paramètres ; le vide d'un grand écran se
// partage. width : 560 px pour un formulaire, 720 px pour un assistant, dont
// la barre d'étapes demande la place.
export function FormPage({ width = "form", children }: { width?: "form" | "wizard"; children: ReactNode }) {
  return <div className={width === "wizard" ? "form-page form-page-wizard" : "form-page"}>{children}</div>;
}
