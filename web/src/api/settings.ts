import { useResource, type Resource } from "../hooks/useResource";
import type { ChannelTest } from "./alerts";
import { api } from "./client";
import type { DiskThresholds } from "./session";

// Les réglages de Paramètres : seuils du disque, relais e-mail, fin de
// support. Seule la fin de support a un sujet, « containers » (topicsOf) ;
// les autres ne se relisent qu'après leur enregistrement ou quand tout se
// relit.

// Les seuils d'alerte du disque ; le retour suit l'attention, et les bornes
// disent la règle au formulaire.
export interface DiskSettings {
  attention: number;
  danger: number;
  recovery: number;
  min_attention: number;
  max_danger: number;
}

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

// Le corps ne porte que les deux seuils, tels que la session les rend.
export type DiskRequest = DiskThresholds;

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
const diskPath = `${settingsPath}/disk`;
const mailPath = `${settingsPath}/mail`;
const lifecyclePath = `${settingsPath}/lifecycle`;

export function useDiskSettings(): Resource<DiskSettings> {
  return useResource<DiskSettings>(diskPath);
}

export function useMailSettings(): Resource<MailSettings> {
  return useResource<MailSettings>(mailPath);
}

export function useLifecycleSettings(): Resource<LifecycleSettings> {
  return useResource<LifecycleSettings>(lifecyclePath);
}

export async function saveDiskSettings(body: DiskRequest): Promise<DiskSettings> {
  return api.put<DiskSettings>(diskPath, body);
}

export async function saveMailSettings(body: MailRequest): Promise<MailSettings> {
  return api.put<MailSettings>(mailPath, body);
}

// Un vrai envoi vers cette adresse, par le relais enregistré.
export async function testMailSettings(to: string): Promise<ChannelTest> {
  return api.post<ChannelTest>(`${mailPath}/actions/test`, { to });
}

export async function saveLifecycleSettings(enabled: boolean): Promise<LifecycleSettings> {
  return api.put<LifecycleSettings>(lifecyclePath, { enabled });
}
