// La fermeture d'une fenêtre modale. Échap, « Annuler » et la croix posent la
// même demande ; le clic sur le voile n'en pose aucune (la saisie ne se perd
// jamais d'un clic à côté). Propre, la fenêtre se ferme. Modifiée, elle
// demande « Abandonner la saisie ? » d'abord. Les transitions sont écrites ici
// pour que la règle se teste sans navigateur.
export type ModalState = "open" | "asking";
export type ModalEvent = "request_close" | "abandon" | "keep";

interface ModalStep {
  state: ModalState;
  // Vrai quand la fenêtre doit se fermer maintenant.
  closes: boolean;
}

export function nextModal(state: ModalState, event: ModalEvent, dirty: boolean): ModalStep {
  if (state === "asking") {
    // Abandonner ferme ; continuer, ou redemander (Échap pendant la question),
    // revient à la saisie.
    return event === "abandon" ? { state: "open", closes: true } : { state: "open", closes: false };
  }
  if (event === "request_close") {
    return dirty ? { state: "asking", closes: false } : { state: "open", closes: true };
  }
  return { state: "open", closes: false };
}
