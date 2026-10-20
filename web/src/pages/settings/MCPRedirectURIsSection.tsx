import { useState } from "react";

import { saveRedirectURIs, type MCPState } from "../../api/mcp";
import { CardHeader } from "../../components/Card";
import { Field, Textarea } from "../../components/Field";
import { FormActions, FormCard } from "../../components/FormCard";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { redirectFormOf, redirectRequestOf } from "../../lib/mcp";

// Les adresses de retour que le client OAuth peut utiliser, une par ligne.
export function MCPRedirectURIsSection({ state, reload }: { state: MCPState; reload: () => void }) {
  const t = useT();
  // Les adresses se lisent une fois, à l'ouverture : une relecture ne doit
  // pas écraser ce que l'opérateur est en train de taper.
  const form = useForm(redirectFormOf(state));
  const { values, change, update } = form;
  const action = useAction();
  const [saved, setSaved] = useState(false);
  const save = () => {
    setSaved(false);
    const uris = redirectRequestOf(values);
    action.run(() => saveRedirectURIs(uris), {
      onDone: (kept) => {
        // La zone reprend les adresses gardées (lignes vides et espaces retirés).
        update(redirectFormOf(kept));
        setSaved(true);
        reload();
      },
    });
  };
  return (
    <FormCard
      onSubmit={save}
      error={action.error}
      header={<CardHeader title={t("mcp.redirect_title")} />}
      actions={
        <FormActions submitLabel={t("mcp.save")} submitIcon="check" busy={action.busy}>
          {saved && <Pill tone="ok">{t("mcp.saved")}</Pill>}
        </FormActions>
      }
    >
      <p className="secondary">{t("mcp.redirect_text")}</p>
      <Field label={t("mcp.redirect_label")} htmlFor="mcp-redirects" help={t("mcp.redirect_help")}>
        <Textarea id="mcp-redirects" mono value={values.uris} onChange={(event) => change("uris", event.target.value)} rows={3} />
      </Field>
    </FormCard>
  );
}
