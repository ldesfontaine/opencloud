import { useState } from "react";

import { saveStatusPageSettings, useStatusPageSettings } from "../../api/status";
import { CardHeader } from "../../components/Card";
import { EnumSelect, Field, Input, Textarea } from "../../components/Field";
import { FormActions, FormCard } from "../../components/FormCard";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useI18n, useT } from "../../i18n/context";
import { emptyStatusPageForm, statusPageFormOf } from "../../lib/status";

// Le titre, l'annonce et la langue de la page : trois champs, un bouton.
export function StatusSettingsTab() {
  const t = useT();
  const { languages } = useI18n();
  const settings = useStatusPageSettings();
  const form = useForm(emptyStatusPageForm, { source: settings.data, fill: statusPageFormOf });
  const { values, change, update } = form;
  const action = useAction();
  const [saved, setSaved] = useState(false);

  const save = () => {
    setSaved(false);
    action.run(() => saveStatusPageSettings(values), {
      onDone: (kept) => {
        // Le formulaire reprend les réglages gardés, tels que le serveur les
        // a rendus.
        update(statusPageFormOf(kept));
        setSaved(true);
        settings.reload();
      },
    });
  };

  return (
    <Loaded resource={settings}>
      {() => (
        <FormCard
          onSubmit={save}
          error={action.error}
          header={<CardHeader title={t("statuspage.settings_title")} aside={t("statuspage.settings_text")} />}
          actions={
            <FormActions submitLabel={t("statuspage.save")} submitIcon="check" busy={action.busy} canSubmit={form.isFilled}>
              {saved && <Pill tone="ok">{t("statuspage.saved")}</Pill>}
            </FormActions>
          }
        >
          <Field label={t("statuspage.title_label")} htmlFor="status-title" help={t("statuspage.title_help")}>
            <Input id="status-title" type="text" value={values.title} onChange={(event) => change("title", event.target.value)} maxLength={120} autoComplete="off" />
          </Field>
          <Field label={t("statuspage.announcement_label")} htmlFor="status-announcement" help={t("statuspage.announcement_help")}>
            <Textarea id="status-announcement" value={values.announcement} onChange={(event) => change("announcement", event.target.value)} maxLength={500} rows={3} />
          </Field>
          <Field label={t("statuspage.language_label")} htmlFor="status-language" help={t("statuspage.language_help")}>
            <EnumSelect id="status-language" value={values.language} values={languages} labelKey="language." onChange={(language) => change("language", language)} />
          </Field>
        </FormCard>
      )}
    </Loaded>
  );
}
