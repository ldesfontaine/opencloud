import { useState } from "react";

import type { ChannelTest } from "../../api/alerts";
import { saveMailSettings, testMailSettings, type MailSettings } from "../../api/settings";
import { Button } from "../../components/Button";
import { Note } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Checkbox, EnumSelect, Input } from "../../components/Field";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingFoot, SettingRow } from "../../components/SettingRow";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { channelTestResult } from "../../lib/alerts";
import { hasChanged } from "../../lib/changes";
import { mailFormOf, mailModeChange, mailRequestOf, mailTLSModes } from "../../lib/mail";
import { SettingsSection } from "./SettingsSection";

// Le relais SMTP du canal e-mail. Le mot de passe ne revient jamais du
// serveur, comme le secret d'un canal ; l'essai part vers une adresse
// saisie, par le relais enregistré. Le relais se lit une fois, à
// l'ouverture : une relecture (le signal de rafraîchissement passe) ne doit
// pas écraser ce que l'opérateur est en train de taper. « Enregistrer »
// attend un écart avec ce que le serveur a gardé : kept, la référence, ne
// bouge qu'avec une réponse du serveur.
export function MailSection({ settings, reload }: { settings: MailSettings; reload: () => void }) {
  const t = useT();
  const form = useForm(mailFormOf(settings));
  const { values, change, update } = form;
  const [kept, setKept] = useState(() => mailFormOf(settings));
  const saving = useAction();
  const testing = useAction();
  const [testTo, setTestTo] = useState("");
  const [saved, setSaved] = useState(false);
  const [test, setTest] = useState<ChannelTest | null>(null);
  const changed = hasChanged(values, kept);
  const busy = saving.busy || testing.busy;

  const save = () => {
    setSaved(false);
    const body = mailRequestOf(values);
    saving.run(() => saveMailSettings(body), {
      onDone: (relay) => {
        // Le formulaire reprend le relais enregistré, tel que le serveur l'a
        // gardé (espaces retirés), et le mot de passe repart vide.
        const relayForm = mailFormOf(relay);
        update(relayForm);
        setKept(relayForm);
        setSaved(true);
        reload();
      },
    });
  };

  const runTest = () => {
    setTest(null);
    testing.run(() => testMailSettings(testTo), { onDone: setTest });
  };

  return (
    <SettingsSection id="e-mails" titleKey="settings.section_mail" text={t("settings.mail_intro")}>
      <form
        className="setting-stack"
        onSubmit={(event) => {
          event.preventDefault();
          save();
        }}
      >
        <SettingRow label={t("settings.mail_host_label")} description={t("settings.mail_host_help")} htmlFor="mail-host" width="medium">
          <Input id="mail-host" mono type="text" value={values.host} onChange={(event) => change("host", event.target.value)} maxLength={253} autoComplete="off" />
        </SettingRow>
        <SettingRow label={t("settings.mail_port_label")} description={t("settings.mail_port_help")} htmlFor="mail-port" width="short">
          <Input id="mail-port" type="number" inputMode="numeric" min={1} max={65535} value={values.port} onChange={(event) => change("port", event.target.value)} />
        </SettingRow>
        <SettingRow label={t("settings.mail_tls_label")} htmlFor="mail-tls">
          <EnumSelect id="mail-tls" value={values.mode} values={mailTLSModes} labelKey="settings.mail_tls_" onChange={(mode) => update(mailModeChange(values, mode))} />
        </SettingRow>
        {values.mode === "none" && (
          <SettingBlock>
            <Note tone="warn">{t("settings.mail_tls_none_warning")}</Note>
          </SettingBlock>
        )}
        <SettingRow label={t("settings.mail_username_label")} description={t("settings.mail_username_help")} htmlFor="mail-username" width="medium">
          <Input id="mail-username" type="text" value={values.username} onChange={(event) => change("username", event.target.value)} maxLength={256} autoComplete="off" />
        </SettingRow>
        <SettingRow
          label={t("settings.mail_password_label")}
          description={settings.has_password ? t("settings.mail_password_keep") : t("settings.mail_password_help")}
          htmlFor="mail-password"
          width="medium"
        >
          <Input id="mail-password" type="password" value={values.password} onChange={(event) => change("password", event.target.value)} maxLength={256} autoComplete="new-password" />
        </SettingRow>
        {settings.has_password && (
          <SettingRow label={t("settings.mail_password_on_file")}>
            <Checkbox label={t("settings.mail_password_clear")} checked={values.clearPassword} onChange={(checked) => change("clearPassword", checked)} />
          </SettingRow>
        )}
        <SettingRow label={t("settings.mail_from_label")} description={t("settings.mail_from_help")} htmlFor="mail-from" width="medium">
          <Input id="mail-from" mono type="email" value={values.from} onChange={(event) => change("from", event.target.value)} maxLength={254} autoComplete="off" />
        </SettingRow>
        {saving.error !== null && (
          <SettingBlock>
            <Failure error={saving.error} />
          </SettingBlock>
        )}
        <SettingFoot note={saved && !changed ? <Pill tone="ok">{t("settings.mail_saved")}</Pill> : null}>
          <Button type="submit" variant="primary" icon="check" disabled={busy || !changed}>
            {t("settings.mail_save")}
          </Button>
        </SettingFoot>
      </form>
      {/* L'essai part par le relais enregistré, pas par la saisie : il a son
          formulaire, Entrée y lance l'essai et non l'enregistrement. */}
      {settings.configured ? (
        <form
          className="setting-stack"
          onSubmit={(event) => {
            event.preventDefault();
            if (!busy && testTo !== "") {
              runTest();
            }
          }}
        >
          <SettingRow label={t("settings.mail_test_label")} htmlFor="mail-test-to" block>
            <Input id="mail-test-to" className="setting-grow" mono type="email" value={testTo} onChange={(event) => setTestTo(event.target.value)} maxLength={254} autoComplete="email" />
            <Button type="submit" icon="refresh" disabled={busy || testTo === ""}>
              {t("settings.mail_test")}
            </Button>
            {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{channelTestResult(t, test)}</Pill>}
          </SettingRow>
          {testing.error !== null && (
            <SettingBlock>
              <Failure error={testing.error} />
            </SettingBlock>
          )}
        </form>
      ) : (
        <SettingBlock>
          <p className="secondary">{t("settings.mail_not_configured")}</p>
        </SettingBlock>
      )}
    </SettingsSection>
  );
}
