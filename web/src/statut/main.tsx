import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { StatusPage } from "./StatusPage";
import "./statut.scss";

// La page publique : son propre bundle, sans routeur, sans coquille, sans
// une ligne de l'administration. Un visiteur ne connaît pas openCloud.
const root = document.getElementById("root");
if (root) {
  createRoot(root).render(
    <StrictMode>
      <StatusPage />
    </StrictMode>,
  );
}
