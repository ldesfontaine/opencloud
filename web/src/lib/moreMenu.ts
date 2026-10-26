// Le menu « … » d'un objet : ce qui le ferme, où va le focus au clavier, où
// il s'ouvre à l'écran. Listé ici pour que la règle se teste sans navigateur.
export type MenuClose = "escape" | "select" | "outside" | "tab" | "scroll";

// Échap et un choix rendent le focus au bouton « … » ; un clic ailleurs, Tab
// ou un défilement le laissent là où l'utilisateur l'a mis.
export function returnsFocus(event: MenuClose): boolean {
  return event === "escape" || event === "select";
}

// L'élément à prendre pour une touche de navigation, ou null pour une autre
// touche. Les flèches bouclent : de la fin on revient au début.
export function nextItemIndex(current: number, count: number, key: string): number | null {
  if (count === 0) {
    return null;
  }
  switch (key) {
    case "ArrowDown":
      return (current + 1) % count;
    case "ArrowUp":
      return (current - 1 + count) % count;
    case "Home":
      return 0;
    case "End":
      return count - 1;
    default:
      return null;
  }
}

interface Anchor {
  top: number;
  bottom: number;
  left: number;
  right: number;
}

interface Size {
  width: number;
  height: number;
}

// Écart au bouton, et marge gardée au bord de la fenêtre.
const menuGap = 4;
const viewportMargin = 8;

// Sous le bouton, aligné sur son bord gauche ; contre son bord droit s'il
// déborderait à droite, au-dessus s'il déborderait en bas et que la place y est.
export function placeMenu(anchor: Anchor, menu: Size, viewport: Size): { top: number; left: number } {
  let left = anchor.left;
  if (left + menu.width > viewport.width - viewportMargin) {
    left = anchor.right - menu.width;
  }
  left = Math.max(viewportMargin, left);

  let top = anchor.bottom + menuGap;
  const above = anchor.top - menuGap - menu.height;
  if (top + menu.height > viewport.height - viewportMargin && above >= viewportMargin) {
    top = above;
  }
  return { top, left };
}
