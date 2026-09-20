import type { Certificate, CertificateThresholds } from "../api/types";
import type { Tone } from "../components/Pill";

// L'état d'un certificat, dérivé ici comme les pastilles de service : le
// serveur rend des faits, le navigateur en fait un mot et une couleur.
//
// L'échéance et la confiance ne se mélangent pas. Un certificat d'autorité
// interne, ou qui couvre le mauvais nom, garde une date parfaitement
// lisible — c'est justement le cas où l'opérateur se fait avoir. « Non
// vérifié » ne remplace donc jamais « À renouveler » : les deux se disent,
// et c'est le plus urgent des deux qui donne la couleur.
export type CertificateState = "valid" | "to_renew" | "expired";

export interface CertificateReading {
  state: CertificateState;
  // Le ton de l'échéance seule ; la confiance a le sien.
  tone: Tone;
  // Les jours restants, arrondis vers le haut : un certificat qui meurt
  // dans douze heures a encore « 1 jour », pas zéro. Négatif une fois
  // l'échéance passée.
  days: number;
  // La chaîne ne remonte à aucune autorité connue de la machine qui sonde,
  // ou le certificat ne couvre pas le nom demandé.
  untrusted: boolean;
  revoked: boolean;
}

const dayMs = 24 * 60 * 60 * 1000;

// daysUntil arrondit vers le haut ce qui reste, vers le bas ce qui est
// passé : « encore 1 jour » tant qu'il reste une heure, « −1 jour » dès
// qu'une heure est passée.
export function daysUntil(at: string, now: number): number {
  const remaining = Date.parse(at) - now;
  return remaining >= 0 ? Math.ceil(remaining / dayMs) : Math.floor(remaining / dayMs);
}

export function read(certificate: Certificate, thresholds: CertificateThresholds, now: number): CertificateReading {
  const days = daysUntil(certificate.not_after, now);
  return {
    state: stateOf(days, thresholds),
    tone: toneOf(days, thresholds),
    days,
    // Une chaîne refusée sur un certificat expiré n'apprend rien : c'est
    // l'échéance qui l'explique, et elle se dit déjà. Le nom, lui, reste
    // une information à part entière dans les deux cas.
    untrusted: (!certificate.chain_valid && days >= 0) || !certificate.hostname_match,
    revoked: certificate.ocsp === "revoked",
  };
}

function stateOf(days: number, thresholds: CertificateThresholds): CertificateState {
  if (days < 0) {
    return "expired";
  }
  return days <= thresholds.warning ? "to_renew" : "valid";
}

// Le ton de l'échéance, et rien d'autre. Une chaîne qu'on ne peut pas
// vérifier ne la rend pas fausse : elle se dit à côté, par sa propre
// pastille, jamais en teintant celle-ci.
function toneOf(days: number, thresholds: CertificateThresholds): Tone {
  if (days <= thresholds.danger) {
    return "danger";
  }
  return days <= thresholds.warning ? "warn" : "ok";
}

// Le certificat le plus pressé d'une liste, pour l'en-tête d'un onglet ou
// la carte de la vue d'ensemble ; rien quand aucun n'a été vu.
export function soonest(certificates: (Certificate | null)[]): Certificate | null {
  let closest: Certificate | null = null;
  for (const candidate of certificates) {
    if (candidate === null) {
      continue;
    }
    if (closest === null || Date.parse(candidate.not_after) < Date.parse(closest.not_after)) {
      closest = candidate;
    }
  }
  return closest;
}
