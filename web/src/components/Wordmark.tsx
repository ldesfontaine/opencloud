import { productName, wordmarkParts } from "../lib/product";

// Le nom du produit en deux poids. Il vient de la page (la balise que le
// serveur remplit), jamais d'un littéral : renommer le produit ne touche pas
// l'écran. Partagé par l'administration et la page de statut publique.
export function Wordmark() {
  const { light, bold } = wordmarkParts(productName());
  return (
    <span className="wordmark">
      <span>{light}</span>
      <b>{bold}</b>
    </span>
  );
}
