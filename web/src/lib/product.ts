// Le nom du produit, tel que le serveur l'a posé dans la page : les chaînes
// passent par les catalogues, mais « le serveur ne répond pas » s'écrit avant
// qu'un catalogue ne soit chargé. Sous « vite dev », le serveur ne remplit
// rien et la balise garde son marqueur.
export function productName(doc: Document = document): string {
  return doc.querySelector('meta[name="application-name"]')?.getAttribute("content") ?? "";
}
