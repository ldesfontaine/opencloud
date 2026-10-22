// Un formulaire a-t-il bougé depuis ce que le serveur a gardé ? Les valeurs
// sont plates (texte, nombre, booléen) : une comparaison champ à champ
// suffit. Sert à désactiver « Enregistrer » tant qu'il n'y a rien à
// enregistrer.
export function hasChanged<Values extends object>(current: Values, saved: Values): boolean {
  const keys = Object.keys(current) as (keyof Values)[];
  return keys.some((key) => current[key] !== saved[key]);
}
