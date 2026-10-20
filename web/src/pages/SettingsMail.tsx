import { useState } from "react";

import type { ChannelTest } from "../api/alerts";
import { saveMailSettings, testMailSettings, type MailSettings } from "../api/settings";
import { Button } from "../components/Button";
import { CardHeader, Note } from "../components/Card";
import { Checkbox, Checks, EnumSelect, Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { Pill } from "../components/Pill";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { channelTestResult } from "../lib/alerts";
import { mailFormOf, mailModeChange, mailRequestOf, mailTLSModes } from "../lib/mail";

// Le relais SMTP du canal e-mail. Le mot de passe ne revient jamais du
// serveur, comme le secret d'un canal ; l'essai part vers une adresse
// saisie, par le relais enregistré. Le relais se lit une fois, à
// l'ouverture : une relecture (le signal de rafraîchissement passe) ne doit
// pas écraser ce que l'opérateur est en train de taper.
export function MailSection({ settings, reload }: { settings: MailSettings; reload: () => void }) {
  const t = useT();
  const form = useForm(mailFormOf(settings));
  const { values, change, update } = form;
  const action = useAction();
  const [testTo, setTestTo] = useState("");
  const [saved, setSaved] = useState(false);
  const [test, setTest] = useState<ChannelTest | null>(null);

  const save = () => {
    setSaved(false);
    const body = mailRequestOf(values);
    action.run(() => saveMailSettings(body), {
      onDone: (relay) => {
        // Le formulaire reprend le relais enregistré, tel que le serveur l'a
        // gardé (espaces retirés), et le mot de passe repart vide.
        update(mailFormOf(relay));
        setSaved(true);
        reload();
      },
    });
  };

  const runTest = () => {
    setTest(null);
    action.run(() => testMailSettings(testTo), { onDone: setTest });
  };

  return (
    <FormCard
      onSubmit={save}
      error={action.error}
      header={<CardHeader title={t("settings.mail_title")} />}
      actions={
        <>
          <FormActions submitLabel={t("settings.mail_save")} submitIcon="check" busy={action.busy}>
            {saved && <Pill tone="ok">{t("settings.mail_saved")}</Pill>}
          </FormActions>
          {/* L'essai vit sous les boutons, dans le même formulaire : il part par le relais enregistré, pas par la saisie. */}
          {settings.configured ? (
            <>
              <Field label={t("settings.mail_test_label")} htmlFor="mail-test-to">
                <Input id="mail-test-to" mono type="email" value={testTo} onChange={(event) => setTestTo(event.target.value)} maxLength={254} autoComplete="email" />
              </Field>
              <div className="cluster">
                <Button icon="refresh" onClick={runTest} disabled={action.busy || testTo === ""}>
                  {t("settings.mail_test")}
                </Button>
                {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{channelTestResult(t, test)}</Pill>}
              </div>
            </>
          ) : (
            <p className="secondary">{t("settings.mail_not_configured")}</p>
          )}
        </>
      }
    >
      <p className="secondary">{t("settings.mail_intro")}</p>
      <div className="grid-2">
        <Field label={t("settings.mail_host_label")} htmlFor="mail-host" help={t("settings.mail_host_help")}>
          <Input id="mail-host" mono type="text" value={values.host} onChange={(event) => change("host", event.target.value)} maxLength={253} autoComplete="off" />
        </Field>
        <Field label={t("settings.mail_port_label")} htmlFor="mail-port" help={t("settings.mail_port_help")}>
          <Input id="mail-port" type="number" inputMode="numeric" min={1} max={65535} value={values.port} onChange={(event) => change("port", event.target.value)} />
        </Field>
      </div>
      <Field label={t("settings.mail_tls_label")} htmlFor="mail-tls">
        <EnumSelect id="mail-tls" value={values.mode} values={mailTLSModes} labelKey="settings.mail_tls_" onChange={(mode) => update(mailModeChange(values, mode))} />
      </Field>
      {values.mode === "none" && <Note tone="warn">{t("settings.mail_tls_none_warning")}</Note>}
      <div className="grid-2">
        <Field label={t("settings.mail_username_label")} htmlFor="mail-username" help={t("settings.mail_username_help")}>
          <Input id="mail-username" type="text" value={values.username} onChange={(event) => change("username", event.target.value)} maxLength={256} autoComplete="off" />
        </Field>
        <Field label={t("settings.mail_password_label")} htmlFor="mail-password" help={settings.has_password ? t("settings.mail_password_keep") : t("settings.mail_password_help")}>
          <Input id="mail-password" type="password" value={values.password} onChange={(event) => change("password", event.target.value)} maxLength={256} autoComplete="new-password" />
        </Field>
      </div>
      {settings.has_password && (
        <Checks>
          <Pill tone="ok" dot={false}>
            {t("settings.mail_password_on_file")}
          </Pill>
          <Checkbox label={t("settings.mail_password_clear")} checked={values.clearPassword} onChange={(checked) => change("clearPassword", checked)} />
        </Checks>
      )}
      <Field label={t("settings.mail_from_label")} htmlFor="mail-from" help={t("settings.mail_from_help")}>
        <Input id="mail-from" mono type="email" value={values.from} onChange={(event) => change("from", event.target.value)} maxLength={254} autoComplete="off" />
      </Field>
    </FormCard>
  );
}
