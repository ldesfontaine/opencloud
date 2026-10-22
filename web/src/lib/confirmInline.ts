// La confirmation en ligne : le bouton d'origine se replie en une question
// avec « Confirmer » et « Annuler ». Les transitions sont listées ici pour
// que la règle se teste sans navigateur.
export type ConfirmState = "idle" | "asking";
export type ConfirmEvent = "ask" | "confirm" | "cancel" | "escape";

export function nextConfirm(state: ConfirmState, event: ConfirmEvent): ConfirmState {
  if (state === "idle" && event === "ask") {
    return "asking";
  }
  // Confirmer, annuler ou Échap referment la question ; hors question, rien
  // d'autre que « ask » ne change l'état.
  return "idle";
}

// Seul « Confirmer », pendant la question, lance l'écriture.
export function shouldConfirm(state: ConfirmState, event: ConfirmEvent): boolean {
  return state === "asking" && event === "confirm";
}

// Le focus revient au bouton d'origine quand la question se referme : sans
// quoi il tomberait sur la page, après un « Annuler » ou un Échap.
export function returnsFocus(before: ConfirmState, after: ConfirmState): boolean {
  return before === "asking" && after === "idle";
}
