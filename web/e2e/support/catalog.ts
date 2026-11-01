import { readFileSync } from "node:fs";

// Les libellés français du produit, lus dans le catalogue du serveur : le
// parcours cherche les boutons par ce que l'opérateur lit, sans le recopier.
const catalogPath = new URL("../../../internal/lang/fr.toml", import.meta.url);

function parseCatalog(source: string): Map<string, string> {
  const entries = new Map<string, string>();
  let section = "";
  for (const line of source.split("\n")) {
    const header = /^\[([\w.]+)\]$/.exec(line.trim());
    if (header?.[1] !== undefined) {
      section = header[1];
      continue;
    }
    const entry = /^(\w+)\s*=\s*"(.*)"\s*$/.exec(line.trim());
    if (entry?.[1] !== undefined && entry[2] !== undefined) {
      entries.set(`${section}.${entry[1]}`, entry[2].replace(/\\"/g, '"'));
    }
  }
  return entries;
}

const catalog = parseCatalog(readFileSync(catalogPath, "utf8"));

export function label(key: string): string {
  const text = catalog.get(key);
  if (text === undefined) {
    throw new Error(`clé de catalogue inconnue : ${key}`);
  }
  return text;
}
