import type { MailTLSMode } from "../api/types";

// Le port qui va avec le mode, comme le serveur : 465 pour TLS dès la
// connexion, 587 sinon.
export function mailPortFor(mode: MailTLSMode): number {
  return mode === "tls" ? 465 : 587;
}

// Les destinataires d'un canal e-mail, tels que le serveur les lit :
// virgules, points-virgules ou espaces, sans doublon.
export function recipientsOf(destination: string): string[] {
  const recipients: string[] = [];
  for (const field of destination.split(/[,;\s]+/)) {
    if (field !== "" && !recipients.includes(field)) {
      recipients.push(field);
    }
  }
  return recipients;
}
