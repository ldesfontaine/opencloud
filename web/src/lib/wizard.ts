// L'assistant pas à pas : des étapes, puis le récapitulatif. Une position est
// l'index d'une étape ; le récapitulatif vient juste après la dernière. Les
// gestes sont écrits ici pour que la règle se teste sans navigateur.
export type WizardMode = "create" | "edit";

// La marque d'une étape dans la barre : faite, courante, à venir.
export type StepMark = "done" | "current" | "todo";

// Ce qu'un déplacement rend : la position atteinte, et la clé de l'erreur de
// l'étape qui l'a arrêté (null quand il est allé au bout).
export interface WizardMove {
  position: number;
  error: string | null;
}

// validate rend la clé du refus d'une étape, ou null quand elle est bonne.
export type StepValidator = (index: number) => string | null;

export function summaryPosition(stepCount: number): number {
  return stepCount;
}

export function isSummary(position: number, stepCount: number): boolean {
  return position === summaryPosition(stepCount);
}

// En création on part de la première étape ; en modification, du
// récapitulatif, d'où l'on va droit à l'étape à changer.
export function initialPosition(mode: WizardMode, stepCount: number): number {
  return mode === "edit" ? summaryPosition(stepCount) : 0;
}

// Revenir en arrière ne demande rien : le brouillon vit chez l'appelant.
// Avancer valide chaque étape franchie, dans l'ordre ; la première en
// erreur arrête le déplacement sur elle, avec son erreur.
export function moveTo(from: number, target: number, validate: StepValidator): WizardMove {
  for (let index = from; index < target; index++) {
    const error = validate(index);
    if (error !== null) {
      return { position: index, error };
    }
  }
  return { position: target, error: null };
}

// Où mène l'action principale d'une étape : la suivante, ou le
// récapitulatif dès qu'il a été vu une fois (on revient d'un « Modifier »,
// sans repasser par les étapes d'après une à une ; elles restent validées
// au passage).
export function forwardTarget(position: number, stepCount: number, reached: number): number {
  const summary = summaryPosition(stepCount);
  if (reached >= summary) {
    return summary;
  }
  return Math.min(position + 1, summary);
}

export function previousPosition(position: number): number {
  return Math.max(position - 1, 0);
}

// La barre d'étapes ne mène directement qu'en modification : en création,
// chaque étape se voit au moins une fois, dans l'ordre.
export function canJumpFromBar(mode: WizardMode): boolean {
  return mode === "edit";
}

// reached est la position la plus avancée déjà atteinte : une étape avant
// elle a passé sa validation, elle est faite.
export function stepMark(index: number, position: number, reached: number): StepMark {
  if (index === position) {
    return "current";
  }
  return index < reached ? "done" : "todo";
}

export function furthestReached(reached: number, position: number): number {
  return Math.max(reached, position);
}
