import { useState } from "react";

import { Field, Input } from "../../components/Field";
import { FormActions, FormBody } from "../../components/FormCard";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { emptyCredentialsForm, passwordFormError } from "../../lib/auth";

// Le formulaire d'activation (identifiant, mot de passe, confirmation) ou
// de changement (mot de passe, confirmation). Il vit dans la carte de
// l'accès, à côté d'autres : FormBody, sans carte de plus.
export function AccessCredentialsForm({
  withUsername,
  minLength,
  busy,
  onSubmit,
}: {
  withUsername: boolean;
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
    <FormBody
      onSubmit={submit}
      error={error}
      actions={<FormActions submitLabel={t(withUsername ? "auth.enable" : "auth.change")} submitIcon={withUsername ? "shield" : "check"} busy={busy} />}
    >
      <p className="field-label">{t(withUsername ? "auth.enable_title" : "auth.change_title")}</p>
      {withUsername && (
        <Field label={t("auth.username_label")} htmlFor="auth-username" help={t("auth.username_help")}>
          <Input id="auth-username" type="text" value={values.username} onChange={(event) => change("username", event.target.value)} maxLength={64} autoComplete="username" required />
        </Field>
      )}
      <Field label={t("auth.password_label")} htmlFor="auth-password" help={t("auth.password_help", minLength)}>
        <Input id="auth-password" type="password" value={values.password} onChange={(event) => change("password", event.target.value)} autoComplete="new-password" required />
      </Field>
      <Field label={t("auth.confirm_label")} htmlFor="auth-confirm">
        <Input id="auth-confirm" type="password" value={values.confirmation} onChange={(event) => change("confirmation", event.target.value)} autoComplete="new-password" required />
      </Field>
      <p className="secondary">{t("auth.browser_prompt")}</p>
    </FormBody>
  );
}
