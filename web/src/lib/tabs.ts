// Les bords d'une barre d'onglets qui cachent encore quelque chose : à gauche
// une fois défilée, à droite tant que la fin n'est pas atteinte. Rien quand
// tout tient. Une tolérance d'un pixel : sur un écran à échelle fractionnaire,
// scrollLeft et scrollWidth ne tombent pas juste.
export interface TabEdges {
  left: boolean;
  right: boolean;
}

const tolerance = 1;

export function edgesOf(scrollLeft: number, scrollWidth: number, clientWidth: number): TabEdges {
  if (scrollWidth - clientWidth <= tolerance) {
    return { left: false, right: false };
  }
  return {
    left: scrollLeft > tolerance,
    right: scrollLeft + clientWidth < scrollWidth - tolerance,
  };
}
