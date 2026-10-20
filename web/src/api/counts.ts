import { useResource, type Resource } from "../hooks/useResource";
import type { AlertCounts } from "./alerts";

// Les compteurs de la barre latérale et de la vue d'ensemble, calculés par
// le serveur. Chemin hors de toute ressource : topicsOf le relit sur tous
// les sujets sauf les mesures.

// Les certificats vus par les sondes actives. expiring et expired sont
// disjoints ; soonest_expires_at est la plus proche échéance à venir, nulle
// quand il n'y en a aucune.
export interface CertificateCounts {
  total: number;
  expiring: number;
  expired: number;
  soonest_expires_at: string | null;
}

export interface Counts {
  machines: { total: number; online: number };
  jobs: { total: number; attention: number };
  // Les conteneurs qui vont mal, et ceux qui ont une image plus récente.
  containers: { total: number; attention: number; updates: number };
  probes: { total: number; attention: number };
  certificates: CertificateCounts;
  // Les incidents ouverts sur la page de statut.
  status: { open_incidents: number };
  // Les alertes ouvertes, et celles que personne n'a acquittées.
  alerts: AlertCounts;
}

export function useCounts(): Resource<Counts> {
  return useResource<Counts>("/api/counts");
}
