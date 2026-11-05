import { useEffect, useState } from "react";

import type { ThresholdRequest, ThresholdRule } from "../api/thresholds";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { hasChanged } from "../lib/changes";
import { isWindowed, thresholdError, thresholdFormOf, thresholdRequestOf } from "../lib/thresholds";
import { Button } from "./Button";
import { Failure } from "./Failure";
import { Input } from "./Field";
import { Pill } from "./Pill";
import { SettingBlock, SettingFoot, SettingRow } from "./SettingRow";

// Le formulaire des seuils d'une ressource : attention, danger, et la fenêtre
// de la moyenne quand la ressource en a une. Le retour n'est pas un champ : il
// suit l'attention, cinq points dessous, et le formulaire le dit.
// « Enregistrer » attend un changement par rapport à ce que le serveur a
// gardé. Le même formulaire sert Paramètres (le réglage global) et la fiche
// d'une machine (sa surcharge) : seule l'écriture change.
export function ThresholdForm({
  rule,
  idPrefix,
  save,
  onSaved,
  saveLabelKey = "thresholds.save",
  onCancel,
}: {
  rule: ThresholdRule;
  idPrefix: string;
  save: (request: ThresholdRequest) => Promise<unknown>;
  // Après l'écriture réussie, et seulement elle.
  onSaved: () => void;
  saveLabelKey?: string;
  onCancel?: () => void;
}) {
  const t = useT();
  const { values, change, update } = useForm(thresholdFormOf(rule));
  const action = useAction();
  // Le refus de la règle, jouée avant l'envoi ; celui du serveur est dans action.
  const [refusal, setRefusal] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  // Les champs suivent la règle quand elle change : celle qu'on vient
  // d'enregistrer, ou celle d'un autre onglet.
  useEffect(() => {
    update(thresholdFormOf(rule));
  }, [rule.attention, rule.danger, rule.window_minutes]);
  const changed = hasChanged(values, thresholdFormOf(rule));
  const windowed = isWindowed(rule);

  const submit = () => {
    setSaved(false);
    const request = thresholdRequestOf(values);
    const refused = thresholdError(request, rule);
    setRefusal(refused);
    if (refused !== null) {
      return;
    }
    action.run(() => save(request), {
      onDone: () => {
        setSaved(true);
        onSaved();
      },
    });
  };
  const error = refusal ?? action.error;
  return (
    <form
      className="setting-stack"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      <SettingRow label={t("thresholds.attention_label")} description={t("thresholds.percent_help", rule.min_attention, rule.max_danger)} htmlFor={`${idPrefix}-attention`} width="short">
        <Input id={`${idPrefix}-attention`} type="number" inputMode="numeric" min={rule.min_attention} max={rule.max_danger - 1} value={values.attention} onChange={(event) => change("attention", event.target.value)} required />
      </SettingRow>
      <SettingRow label={t("thresholds.danger_label")} htmlFor={`${idPrefix}-danger`} width="short">
        <Input id={`${idPrefix}-danger`} type="number" inputMode="numeric" min={rule.min_attention + 1} max={rule.max_danger} value={values.danger} onChange={(event) => change("danger", event.target.value)} required />
      </SettingRow>
      {windowed ? (
        <SettingRow label={t("thresholds.window_label")} description={t("thresholds.window_help", rule.min_window_minutes, rule.max_window_minutes)} htmlFor={`${idPrefix}-window`} width="short">
          <Input id={`${idPrefix}-window`} type="number" inputMode="numeric" min={rule.min_window_minutes} max={rule.max_window_minutes} value={values.window} onChange={(event) => change("window", event.target.value)} required />
        </SettingRow>
      ) : (
        <SettingBlock>
          <p className="secondary">{t("thresholds.window_instant")}</p>
        </SettingBlock>
      )}
      <SettingBlock>
        <p className="secondary">{t("thresholds.recovery", rule.recovery)}</p>
      </SettingBlock>
      {error !== null && (
        <SettingBlock>
          <Failure error={error} />
        </SettingBlock>
      )}
      <SettingFoot
        note={
          <>
            {/* La portée du réglage reste dite, « Enregistré » ou non. */}
            {saved && !changed && <Pill tone="ok">{t("thresholds.saved")}</Pill>}
            <span>{t("thresholds.applies")}</span>
          </>
        }
      >
        {onCancel !== undefined && (
          <Button type="button" onClick={onCancel} disabled={action.busy}>
            {t("thresholds.cancel")}
          </Button>
        )}
        <Button type="submit" variant="primary" icon="check" disabled={action.busy || !changed}>
          {t(saveLabelKey)}
        </Button>
      </SettingFoot>
    </form>
  );
}
