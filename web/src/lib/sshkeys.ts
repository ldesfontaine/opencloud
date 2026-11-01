import type { SSHKey, SSHReport } from "../api/sshkeys";

// Le seuil sous lequel une clé RSA est tenue pour faible.
const minRSABits = 3072;

// Une clé est faible quand elle est DSA, ou RSA sous 3072 bits.
export function isWeak(key: Pick<SSHKey, "type" | "bits">): boolean {
  return key.type === "ssh-dss" || (key.type === "ssh-rsa" && key.bits < minRSABits);
}

// L'empreinte sans son préfixe, raccourcie : de quoi reconnaître une clé d'un
// coup d'œil, le titre de la cellule portant l'empreinte entière.
export function shortFingerprint(fingerprint: string): string {
  const body = fingerprint.startsWith("SHA256:") ? fingerprint.slice("SHA256:".length) : fingerprint;
  return body.length > 12 ? `${body.slice(0, 12)}…` : body;
}

// Vrai quand au moins un compte de la machine n'a pas pu être lu parce que
// l'agent n'en avait pas le droit : la phrase sur les comptes dédiés se justifie.
export function hasDenied(report: SSHReport): boolean {
  return report.accounts.some((account) => account.state === "denied");
}
