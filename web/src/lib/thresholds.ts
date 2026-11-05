import type { ResourceName } from "../api/session";
import type { ThresholdRequest, ThresholdRule } from "../api/thresholds";
import type { Translate } from "../i18n/context";

// Les ressources dans l'ordre des écrans.
export const resourceOrder: readonly ResourceName[] = ["cpu", "memory", "disk"];

// Le serveur refuse hors de ces bornes (alert.Rule.Validate) ; le même refus
// est joué avant l'envoi.
const thresholdInvalidKey = "settings.threshold_invalid";

export function isWindowed(rule: Pick<ThresholdRule, "max_window_minutes">): boolean {
  return rule.max_window_minutes > 0;
}

// La règle du serveur, rejouée avant l'envoi : des entiers, l'attention
// d'au moins min_attention, le danger d'au plus max_danger, l'attention sous
// le danger, et une fenêtre dans ses bornes quand la ressource en a une.
// Rend la clé du refus, ou null.
export function thresholdError(request: ThresholdRequest, rule: ThresholdRule): string | null {
  const { attention, danger, window_minutes: window } = request;
  if (!Number.isInteger(attention) || !Number.isInteger(danger) || !Number.isInteger(window)) {
    return thresholdInvalidKey;
  }
  if (attention < rule.min_attention || danger > rule.max_danger || attention >= danger) {
    return thresholdInvalidKey;
  }
  if (!isWindowed(rule)) {
    return window === 0 ? null : thresholdInvalidKey;
  }
  return window < rule.min_window_minutes || window > rule.max_window_minutes ? thresholdInvalidKey : null;
}

// Les valeurs telles que les champs les montrent : du texte, jusqu'à l'envoi.
interface ThresholdFormValues {
  attention: string;
  danger: string;
  window: string;
}

export function thresholdFormOf(rule: Pick<ThresholdRule, "attention" | "danger" | "window_minutes">): ThresholdFormValues {
  return { attention: String(rule.attention), danger: String(rule.danger), window: String(rule.window_minutes) };
}

export function thresholdRequestOf(values: ThresholdFormValues): ThresholdRequest {
  return { attention: Number(values.attention), danger: Number(values.danger), window_minutes: Number(values.window) };
}

// La règle dite en une ligne : « attention à 95 %, danger à 99 %, moyenne sur
// 15 min » ; un volume n'a pas de fenêtre.
export function ruleSentence(t: Translate, rule: Pick<ThresholdRule, "attention" | "danger" | "window_minutes">): string {
  if (rule.window_minutes === 0) {
    return t("thresholds.rule_instant", rule.attention, rule.danger);
  }
  return t("thresholds.rule_windowed", rule.attention, rule.danger, rule.window_minutes);
}
