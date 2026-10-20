import { Field, Input } from "../../components/Field";
import { FormActions, FormBody } from "../../components/FormCard";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { defaultForwardHeader } from "../../lib/auth";

// Le formulaire du mode mandataire : le nom de l'en-tête d'identité,
// Remote-User pour Authelia. Le serveur n'accepte que si la requête qui
// active le porte déjà, depuis un mandataire de confiance. Comme celui des
// identifiants, il vit dans la carte de l'accès ; l'erreur de l'écriture
// s'y dit, plus haut.
export function AccessForwardForm({ busy, onSubmit }: { busy: boolean; onSubmit: (header: string) => void }) {
  const t = useT();
  const { values, change } = useForm({ header: defaultForwardHeader });
  return (
    <FormBody
      onSubmit={() => onSubmit(values.header.trim())}
      error={null}
      actions={<FormActions submitLabel={t("auth.enable")} submitIcon="shield" busy={busy} />}
    >
      <p className="field-label">{t("auth.enable_title")}</p>
      <Field label={t("auth.header_label")} htmlFor="auth-header" help={t("auth.header_help")}>
        <Input id="auth-header" type="text" mono value={values.header} onChange={(event) => change("header", event.target.value)} maxLength={64} required />
      </Field>
      <p className="secondary">{t("auth.forward_guard")}</p>
    </FormBody>
  );
}
