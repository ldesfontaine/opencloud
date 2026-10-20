import type { MailRequest, MailSettings, MailTLSMode } from "../api/settings";

// Les modes de chiffrement du relais, dans l'ordre du sélecteur.
export const mailTLSModes: readonly MailTLSMode[] = ["starttls", "tls", "none"];

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

// Le relais tel que l'opérateur le tape. Le mot de passe ne revient jamais
// du serveur : le champ part vide, et vide il garde celui qui est
// enregistré ; clearPassword le retire.
export interface MailFormValues {
  host: string;
  port: string;
  mode: MailTLSMode;
  username: string;
  password: string;
  clearPassword: boolean;
  from: string;
}

export function mailFormOf(settings: MailSettings): MailFormValues {
  return {
    host: settings.host,
    port: String(settings.port),
    mode: settings.tls,
    username: settings.username,
    password: "",
    clearPassword: false,
    from: settings.from,
  };
}

export function mailRequestOf(values: MailFormValues): MailRequest {
  return {
    host: values.host,
    port: Number(values.port),
    tls: values.mode,
    username: values.username,
    password: values.password,
    clear_password: values.clearPassword,
    from: values.from,
  };
}

// Changer de mode propose son port, sauf si l'opérateur en a tapé un autre.
export function mailModeChange(values: MailFormValues, mode: MailTLSMode): Partial<MailFormValues> {
  if (values.port === String(mailPortFor(values.mode))) {
    return { mode, port: String(mailPortFor(mode)) };
  }
  return { mode };
}
