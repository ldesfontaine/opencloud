import { readFileSync } from "node:fs";

import type { NetworkMap } from "../../src/api/network";

// Les cartes de démonstration : la réponse exacte de GET /api/network pour les
// jeux que internal/server fige (api_network_map_demo_test.go, noms et adresses
// génériques). Le parcours les sert à la place de la réponse du serveur pour
// les cas que les données fictives du serveur ne montrent pas ; le serveur,
// lui, prouve la déduction (internal/netmap). Aucune forme n'est écrite ici.

function golden(name: string): NetworkMap {
  const file = new URL(`../../../internal/server/testdata/network_${name}.golden.json`, import.meta.url);
  return JSON.parse(readFileSync(file, "utf8")) as NetworkMap;
}

// La forme d'un vrai réseau : treize interfaces dont la boucle locale, le WAN et
// un tunnel ; un VPS de type inconnu à adresse publique ; un hôte physique et
// ses dix invités sur cinq zones ; le Pi sans service ; deux entrées publiques.
export const demoMap = (): NetworkMap => golden("demo");

// Sans Proxmox, sans pare-feu, sans VPS : des machines à plat, des zones de
// sous-réseau.
export const flatMap = (): NetworkMap => golden("flat");

// Quarante machines sur trois hôtes : de quoi montrer le repli.
export const bigMap = (): NetworkMap => golden("big");
