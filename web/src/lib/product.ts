// Le nom du produit, tel que le serveur l'a posé dans la page : les chaînes
// passent par les catalogues, mais « le serveur ne répond pas » s'écrit avant
// qu'un catalogue ne soit chargé. Sous « vite dev », le serveur ne remplit
// rien et la balise garde son marqueur.
export function productName(doc: Document = document): string {
  return doc.querySelector('meta[name="application-name"]')?.getAttribute("content") ?? "";
}

// Le mot-symbole s'écrit en deux poids : « open » en clair, « Cloud » en
// gras. La coupure suit la première majuscule après la tête du nom ; un nom
// sans majuscule intérieure s'écrit d'un seul poids.
export function wordmarkParts(name: string): { light: string; bold: string } {
  const cut = name.slice(1).search(/[A-Z]/);
  if (cut < 0) {
    return { light: name, bold: "" };
  }
  return { light: name.slice(0, cut + 1), bold: name.slice(cut + 1) };
}
