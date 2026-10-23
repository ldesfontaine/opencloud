import { useState } from "react";

import { Field, Input } from "../../components/Field";
import { FormActions, FormBody } from "../../components/FormCard";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { emptyCredentialsForm, passwordFormError } from "../../lib/auth";

// Le formulaire de création du compte, à la première configuration
// (identifiant, mot de passe, confirmation). Il vit dans une carte, à côté
// d'autres : FormBody, sans carte de plus. Changer le mot de passe sous
// Paramètres a le sien : AccessPasswordForm.
export function AccessCredentialsForm({
  minLength,
  busy,
  onSubmit,
}: {
  minLength: number;
  busy: boolean;
  onSubmit: (username: string, password: string) => void;
}) {
  const t = useT();
  const { values, change } = useForm(emptyCredentialsForm);
  const [error, setError] = useState<string | null>(null);
  const submit = () => {
    const refusal = passwordFormError(values.password, values.confirmation, minLength);
    setError(refusal);
    if (refusal === null) {
      onSubmit(values.username, values.password);
    }
  };
  return (
    <FormBody onSubmit={submit} error={error} actions={<FormActions submitLabel={t("auth.setup_create")} submitIcon="shield" busy={busy} />}>
      <p className="field-label">{t("auth.setup_account")}</p>
      <Field label={t("auth.username_label")} htmlFor="auth-username" help={t("auth.username_help")}>
        <Input id="auth-username" type="text" value={values.username} onChange={(event) => change("username", event.target.value)} maxLength={64} autoComplete="username" required />
      </Field>
      <Field label={t("auth.password_label")} htmlFor="auth-password" help={t("auth.password_help", minLength)}>
        <Input id="auth-password" type="password" value={values.password} onChange={(event) => change("password", event.target.value)} autoComplete="new-password" required />
      </Field>
      <Field label={t("auth.confirm_label")} htmlFor="auth-confirm">
        <Input id="auth-confirm" type="password" value={values.confirmation} onChange={(event) => change("confirmation", event.target.value)} autoComplete="new-password" required />
      </Field>
    </FormBody>
  );
}
