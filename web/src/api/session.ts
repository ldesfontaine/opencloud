import { api, segment } from "./client";

// La session du navigateur : version, langue, et les seuils que le serveur
// tient pour que les écrans comptent comme lui. Lue une fois par i18n, avant
// tout affichage : pas de hook de lecture ici.

export interface CertificateThresholds {
  warning: number;
  danger: number;
}

export interface DiskThresholds {
  attention: number;
  danger: number;
}

export interface Session {
  version: string;
  language: string;
  languages: string[];
  // Les jours à partir desquels un certificat se signale. Le serveur les
  // tient : le compteur et la couleur d'une ligne basculent sur le même
  // chiffre.
  certificate_thresholds: CertificateThresholds;
  // Les pourcentages à partir desquels un volume alerte, puis s'aggrave.
  disk_thresholds: DiskThresholds;
}

// Un catalogue : la clé, et son format Go (%s, %d).
export type Catalog = Record<string, string>;

const sessionPath = "/api/session";

export async function fetchSession(): Promise<Session> {
  return api.get<Session>(sessionPath);
}

export async function fetchCatalog(language: string): Promise<Catalog> {
  return api.get<Catalog>(`/api/i18n/${segment(language)}`);
}

export async function saveLanguage(language: string): Promise<void> {
  return api.put(`${sessionPath}/language`, { language });
}
