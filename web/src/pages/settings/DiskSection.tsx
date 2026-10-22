import { useEffect, useState } from "react";

import { saveDiskSettings, type DiskSettings } from "../../api/settings";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Input } from "../../components/Field";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingFoot, SettingRow } from "../../components/SettingRow";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useI18n, useT } from "../../i18n/context";
import { hasChanged } from "../../lib/changes";
import { diskFormOf, diskRequestOf, diskSettingsError } from "../../lib/disks";
import { SettingsSection } from "./SettingsSection";

// Les seuils d'alerte du disque. Le retour n'est pas un champ : il suit
// l'attention, cinq points dessous, et la section le dit. « Enregistrer »
// attend un changement par rapport aux seuils que le serveur a gardés.
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
  const changed = hasChanged(values, diskFormOf(settings));

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
        // Les jauges lisent les seuils dans la session : elle se relit avec
        // eux. Si elle échoue, les seuils n'en sont pas moins enregistrés : la
        // pastille « enregistré » reste, l'erreur s'ajoute.
        action.run(reloadSession);
      },
      // Après chaque réponse, refus compris : ce que le serveur garde est la
      // référence de « rien à enregistrer ».
      onSettled: reload,
    });
  };
  const error = refusal ?? action.error;
  return (
    <SettingsSection id="alertes" titleKey="settings.section_alerts" text={t("settings.disk_intro")}>
      <form
        className="setting-stack"
        onSubmit={(event) => {
          event.preventDefault();
          save();
        }}
      >
        <SettingRow label={t("settings.disk_attention_label")} description={t("settings.disk_percent_help", settings.min_attention, settings.max_danger)} htmlFor="disk-attention" width="short">
          <Input id="disk-attention" type="number" inputMode="numeric" min={settings.min_attention} max={settings.max_danger - 1} value={values.attention} onChange={(event) => change("attention", event.target.value)} required />
        </SettingRow>
        <SettingRow label={t("settings.disk_danger_label")} htmlFor="disk-danger" width="short">
          <Input id="disk-danger" type="number" inputMode="numeric" min={settings.min_attention + 1} max={settings.max_danger} value={values.danger} onChange={(event) => change("danger", event.target.value)} required />
        </SettingRow>
        <SettingBlock>
          <p className="secondary">{t("settings.disk_recovery", settings.recovery)}</p>
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
              {saved && !changed && <Pill tone="ok">{t("settings.disk_saved")}</Pill>}
              <span>{t("settings.disk_applies")}</span>
            </>
          }
        >
          <Button type="submit" variant="primary" icon="check" disabled={action.busy || !changed}>
            {t("settings.disk_save")}
          </Button>
        </SettingFoot>
      </form>
    </SettingsSection>
  );
}
