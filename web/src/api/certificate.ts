import { api } from "./client";
import { route } from "./routes";

// Ce qu'une adresse https répond au handshake TLS, sans clé : joignable ou
// non, et le certificat qu'elle présente. Le serveur joint l'adresse (le
// navigateur ne le peut pas), pour les assistants Pare-feu et Proxmox.

type CertificateOutcome = "trusted" | "untrusted" | "unreachable";

// Pourquoi une adresse est injoignable (seulement pour « unreachable »).
type UnreachableCause = "timeout" | "refused" | "unresolved" | "no_route" | "forbidden" | "not_tls" | "other";

export interface CertificateInspection {
  outcome: CertificateOutcome;
  cause: UnreachableCause | "";
  // SHA-256 du certificat, en hexadécimal minuscule sans séparateur ; vide si injoignable.
  fingerprint: string;
  subject: string;
  not_after: string | null;
}

export function inspectCertificate(url: string): Promise<CertificateInspection> {
  return api.send<CertificateInspection>(route("POST /api/certificate/inspect"), { url });
}
