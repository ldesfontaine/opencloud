// Le support d'un logiciel (système d'une machine, image d'un conteneur),
// tel que le serveur le lit d'une source publique. Aucun chemin à lui : il
// arrive dans la machine ou le conteneur, quand la fin de support est
// activée sous Paramètres (api/settings).

export type LifecycleStatus = "maintained" | "eol" | "unknown_product" | "unknown_version" | "pending";

// Une branche maintenue plus récente que celle qui tourne. Les dates sont
// des jours, « 2028-11-09 », vides quand la source n'en annonce pas.
export interface NewerCycle {
  cycle: string;
  latest: string;
  eol_from: string;
}

// Ce que la source dit du support d'une image : le produit reconnu, la
// branche qui tourne, jusqu'à quand, et vers quoi monter.
export interface Lifecycle {
  status: LifecycleStatus;
  product: string;
  product_url: string;
  cycle: string;
  eol_from: string;
  latest: string;
  newer: NewerCycle[];
  fetched_at: string | null;
  is_stale: boolean;
}
