// La ligne cliquable d'un tableau : un clic n'importe où sur la ligne suit le
// lien de son nom. Listé ici pour que la règle se teste sans navigateur.
export type RowClick = "ignore" | "follow" | "new-tab";

export interface RowClickFacts {
  // 0 le bouton principal, 1 celui du milieu.
  button: number;
  // Ctrl, Cmd ou Maj : l'utilisateur demande un autre onglet.
  withModifier: boolean;
  // Le clic tombe sur un lien, un bouton, un champ ou le menu « … » de la
  // ligne : c'est lui qui répond, pas la ligne.
  onControl: boolean;
  // L'utilisateur sélectionne du texte dans la ligne pour le copier.
  selectingText: boolean;
}

export function rowClickAction(click: RowClickFacts): RowClick {
  if (click.onControl || click.selectingText) {
    return "ignore";
  }
  if (click.button === 1) {
    return "new-tab";
  }
  if (click.button !== 0) {
    return "ignore";
  }
  return click.withModifier ? "new-tab" : "follow";
}
