import { useEffect, useState } from "react";

import { saveDiskSettings, type DiskSettings } from "../../api/settings";
import { CardHeader } from "../../components/Card";
import { Field, Input } from "../../components/Field";
import { FormActions, FormCard } from "../../components/FormCard";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useI18n, useT } from "../../i18n/context";
import { diskFormOf, diskRequestOf, diskSettingsError } from "../../lib/disks";

// Les seuils d'alerte du disque. Le retour n'est pas un champ : il suit
// l'attention, cinq points dessous, et la carte le dit.
export function DiskSection({ settings, reload }: { settings: DiskSettings; reload: () => void }) {
  const t = useT();
  const { reloadSession } = useI18n();
  const { values, change, update } = useForm(diskFormOf(settings));
  const action = useAction();
  // Le refus de la règle, jouée avant l'envoi ; celui du serveur est dans action.
  const [refusal, setRefusal] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  // Les champs suivent les seuils quand ils changent : ceux qu'on vient
  // d'enregistrer, ou ceux d'un autre onglet.
  useEffect(() => {
    update(diskFormOf(settings));
  }, [settings.attention, settings.danger]);

  const save = () => {
    setSaved(false);
    const thresholds = diskRequestOf(values);
    const refused = diskSettingsError(thresholds, settings);
    setRefusal(refused);
    if (refused !== null) {
      return;
    }
    action.run(() => saveDiskSettings(thresholds), {
      onDone: () => {
        setSaved(true);
        reload();
        // Les jauges lisent les seuils dans la session : elle se relit avec
        // eux. Si elle échoue, les seuils n'en sont pas moins enregistrés : la
        // pastille « enregistré » reste, l'erreur s'ajoute.
        action.run(reloadSession);
      },
    });
  };
  const help = t("settings.disk_percent_help", settings.min_attention, settings.max_danger);
  return (
    <FormCard
      onSubmit={save}
      error={refusal ?? action.error}
      header={<CardHeader title={t("settings.disk_title")} />}
      actions={
        <FormActions submitLabel={t("settings.disk_save")} submitIcon="check" busy={action.busy}>
          {saved && <Pill tone="ok">{t("settings.disk_saved")}</Pill>}
        </FormActions>
      }
    >
      <p className="secondary">{t("settings.disk_intro")}</p>
      <Field label={t("settings.disk_attention_label")} htmlFor="disk-attention" help={help}>
        <Input id="disk-attention" type="number" inputMode="numeric" min={settings.min_attention} max={settings.max_danger - 1} value={values.attention} onChange={(event) => change("attention", event.target.value)} required />
      </Field>
      <Field label={t("settings.disk_danger_label")} htmlFor="disk-danger">
        <Input id="disk-danger" type="number" inputMode="numeric" min={settings.min_attention + 1} max={settings.max_danger} value={values.danger} onChange={(event) => change("danger", event.target.value)} required />
      </Field>
      <p className="secondary">{t("settings.disk_recovery", settings.recovery)}</p>
      <p className="secondary">{t("settings.disk_applies")}</p>
    </FormCard>
  );
}
