// Le menu en tiroir du téléphone : ouvert ou fermé, rien d'autre. Ce qui le
// ferme est listé ici pour que la règle se teste sans navigateur.
export type DrawerEvent = "toggle" | "backdrop" | "escape" | "navigate" | "wide";

export function nextDrawer(open: boolean, event: DrawerEvent): boolean {
  if (event === "toggle") {
    return !open;
  }
  // Le voile, Échap, une page changée, un écran redevenu large : on ferme.
  return false;
}

// Le focus ne revient au bouton que si le tiroir se fermait vraiment et que
// la fermeture vient de l'utilisateur : passer en écran large ne vole pas
// le focus.
export function returnsFocus(wasOpen: boolean, event: DrawerEvent): boolean {
  return wasOpen && event !== "toggle" && event !== "wide";
}
