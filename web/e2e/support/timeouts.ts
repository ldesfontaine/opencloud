// Les délais du parcours, en un seul endroit. Un test attend l'état visé
// jusqu'à ces bornes ; elles tiennent une machine chargée, pas une machine
// calme. Un délai plus court codé en dur dans un test fait tomber tout le
// parcours dès qu'un chargement traîne.

// Un geste (clic, saisie, apparition d'un élément) ; c'est aussi le délai de
// chaque attente d'un état de la page.
export const actionTimeout = 10_000;

// Une navigation, et l'écran principal qui se pose derrière.
export const pageLoadTimeout = 60_000;
