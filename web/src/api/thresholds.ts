import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";
import type { ResourceName } from "./session";

// Les seuils des alertes de ressources : une règle par ressource, au niveau
// global (Paramètres) ou pour une machine (sa fiche). Le serveur dit d'où
// vient chaque règle ; l'écran le répète.

// global : le réglage de Paramètres ; machine : la surcharge de la fiche ;
// default : rien n'est réglé, les valeurs du produit.
type ThresholdOrigin = "global" | "machine" | "default";

// Ce que la machine suivrait sans sa surcharge.
interface GlobalValues {
  attention: number;
  danger: number;
  window_minutes: number;
}

export interface ThresholdRule {
  resource: ResourceName;
  attention: number;
  danger: number;
  recovery: number;
  // 0 : jugé sur la dernière mesure (le disque), sans fenêtre.
  window_minutes: number;
  origin: ThresholdOrigin;
  // Les bornes disent la règle au formulaire sans la recopier.
  min_attention: number;
  max_danger: number;
  min_window_minutes: number;
  max_window_minutes: number;
  global?: GlobalValues;
}

export interface ThresholdRules {
  rules: ThresholdRule[];
}

export interface ThresholdRequest {
  attention: number;
  danger: number;
  window_minutes: number;
}

// Le rappel d'une alerte de danger restée sans lecture ; 0 le coupe.
export interface ReminderSettings {
  hours: number;
  max_hours: number;
}

const thresholdsPath = "/api/settings/thresholds";
const reminderPath = "/api/settings/reminder";

function machineThresholdsPath(machineID: string): string {
  return `/api/machines/${segment(machineID)}/thresholds`;
}

export function useThresholdRules(): Resource<ThresholdRules> {
  return useResource<ThresholdRules>(thresholdsPath);
}

// machineID nul : rien à lire.
export function useMachineThresholds(machineID: string | null): Resource<ThresholdRules> {
  return useResource<ThresholdRules>(machineID === null ? null : () => machineThresholdsPath(machineID));
}

export function useReminderSettings(): Resource<ReminderSettings> {
  return useResource<ReminderSettings>(reminderPath);
}

export async function saveThreshold(resource: ResourceName, body: ThresholdRequest): Promise<ThresholdRules> {
  return api.send<ThresholdRules>(route("PUT /api/settings/thresholds/{resource}", { resource }), body);
}

export async function saveMachineThreshold(machineID: string, resource: ResourceName, body: ThresholdRequest): Promise<ThresholdRules> {
  return api.send<ThresholdRules>(route("PUT /api/machines/{id}/thresholds/{resource}", { id: machineID, resource }), body);
}

// La machine suit de nouveau le réglage global pour cette ressource.
export async function clearMachineThreshold(machineID: string, resource: ResourceName): Promise<ThresholdRules> {
  return api.send<ThresholdRules>(route("DELETE /api/machines/{id}/thresholds/{resource}", { id: machineID, resource }));
}

export async function saveReminderSettings(hours: number): Promise<ReminderSettings> {
  return api.send<ReminderSettings>(route("PUT /api/settings/reminder"), { hours });
}
