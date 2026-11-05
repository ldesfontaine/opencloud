import { useResource, type Resource } from "../hooks/useResource";
import type { ChannelTest } from "./alerts";
import { api } from "./client";
import { route } from "./routes";

// Les réglages de Paramètres : relais e-mail, fin de support (les seuils et
// le rappel sont dans api/thresholds). Seule la fin de support a un sujet, « containers » (topicsOf) ;
// les autres ne se relisent qu'après leur enregistrement ou quand tout se
// relit.

export type MailTLSMode = "starttls" | "tls" | "none";

// Le relais SMTP du canal e-mail ; le mot de passe ne revient jamais.
export interface MailSettings {
  configured: boolean;
  host: string;
  port: number;
  tls: MailTLSMode;
  username: string;
  has_password: boolean;
  from: string;
}

// Le réglage de la fin de support, et ce que la dernière lecture a donné.
export interface LifecycleSettings {
  enabled: boolean;
  source: string;
  fetched_at: string | null;
  products: number;
  is_failing: boolean;
  last_attempt_at: string | null;
}

// Le mot de passe vide garde l'ancien ; clear_password l'efface.
export interface MailRequest {
  host: string;
  port: number;
  tls: MailTLSMode;
  username: string;
  password: string;
  clear_password: boolean;
  from: string;
}

const settingsPath = "/api/settings";
const mailPath = `${settingsPath}/mail`;
const lifecyclePath = `${settingsPath}/lifecycle`;

export function useMailSettings(): Resource<MailSettings> {
  return useResource<MailSettings>(mailPath);
}

export function useLifecycleSettings(): Resource<LifecycleSettings> {
  return useResource<LifecycleSettings>(lifecyclePath);
}

export async function saveMailSettings(body: MailRequest): Promise<MailSettings> {
  return api.send<MailSettings>(route("PUT /api/settings/mail"), body);
}

// Un vrai envoi vers cette adresse, par le relais enregistré.
export async function testMailSettings(to: string): Promise<ChannelTest> {
  return api.send<ChannelTest>(route("POST /api/settings/mail/actions/test"), { to });
}

export async function saveLifecycleSettings(enabled: boolean): Promise<LifecycleSettings> {
  return api.send<LifecycleSettings>(route("PUT /api/settings/lifecycle"), { enabled });
}
