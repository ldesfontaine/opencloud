import { useEffect, useState, type FormEvent } from "react";

import { api, errorKey } from "../api/client";
import type { ChannelTest, MailSettings, MailTLSMode } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardHeader, Note } from "../components/Card";
import { Field, Input, Select } from "../components/Field";
import { Pill } from "../components/Pill";
import { useT } from "../i18n/context";
import { channelTestResult } from "../lib/alerts";
import { mailPortFor } from "../lib/mail";

const modes: readonly MailTLSMode[] = ["starttls", "tls", "none"];

// Le relais SMTP du canal e-mail. Le mot de passe ne revient jamais du
// serveur, comme le secret d'un canal ; l'essai part vers une adresse
// saisie, par le relais enregistré.
export function MailSection({ settings, reload }: { settings: MailSettings; reload: () => void }) {
  const t = useT();
  const [host, setHost] = useState(settings.host);
  const [port, setPort] = useState(String(settings.port));
  const [mode, setMode] = useState<MailTLSMode>(settings.tls);
  const [username, setUsername] = useState(settings.username);
  const [password, setPassword] = useState("");
  const [clearPassword, setClearPassword] = useState(false);
  const [from, setFrom] = useState(settings.from);
  const [testTo, setTestTo] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const [test, setTest] = useState<ChannelTest | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setHost(settings.host);
    setPort(String(settings.port));
    setMode(settings.tls);
    setUsername(settings.username);
    setFrom(settings.from);
  }, [settings]);

  // Changer de mode propose son port, sauf si l'opérateur en a tapé un autre.
  const changeMode = (next: MailTLSMode) => {
    if (port === String(mailPortFor(mode))) {
      setPort(String(mailPortFor(next)));
    }
    setMode(next);
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setSaved(false);
    setError(null);
    setBusy(true);
    const body = { host, port: Number(port), tls: mode, username, password, clear_password: clearPassword, from };
    api
      .put<MailSettings>("/api/settings/mail", body)
      .then(() => {
        setSaved(true);
        setPassword("");
        setClearPassword(false);
        reload();
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  const runTest = () => {
    setBusy(true);
    setTest(null);
    setError(null);
    api
      .post<ChannelTest>("/api/settings/mail/actions/test", { to: testTo })
      .then(setTest)
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <Card className="form-card">
      <CardHeader title={t("settings.mail_title")} />
      <form className="card-b" onSubmit={submit}>
        <p className="secondary">{t("settings.mail_intro")}</p>
        <div className="grid-2">
          <Field label={t("settings.mail_host_label")} htmlFor="mail-host" help={t("settings.mail_host_help")}>
            <Input id="mail-host" mono type="text" value={host} onChange={(event) => setHost(event.target.value)} maxLength={253} autoComplete="off" />
          </Field>
          <Field label={t("settings.mail_port_label")} htmlFor="mail-port" help={t("settings.mail_port_help")}>
            <Input id="mail-port" type="number" inputMode="numeric" min={1} max={65535} value={port} onChange={(event) => setPort(event.target.value)} />
          </Field>
        </div>
        <Field label={t("settings.mail_tls_label")} htmlFor="mail-tls">
          <Select id="mail-tls" value={mode} onChange={(event) => changeMode(event.target.value as MailTLSMode)}>
            {modes.map((candidate) => (
              <option key={candidate} value={candidate}>
                {t(`settings.mail_tls_${candidate}`)}
              </option>
            ))}
          </Select>
        </Field>
        {mode === "none" && <Note tone="warn">{t("settings.mail_tls_none_warning")}</Note>}
        <div className="grid-2">
          <Field label={t("settings.mail_username_label")} htmlFor="mail-username" help={t("settings.mail_username_help")}>
            <Input id="mail-username" type="text" value={username} onChange={(event) => setUsername(event.target.value)} maxLength={256} autoComplete="off" />
          </Field>
          <Field label={t("settings.mail_password_label")} htmlFor="mail-password" help={settings.has_password ? t("settings.mail_password_keep") : t("settings.mail_password_help")}>
            <Input id="mail-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} maxLength={256} autoComplete="new-password" />
          </Field>
        </div>
        {settings.has_password && (
          <div className="checks">
            <Pill tone="ok" dot={false}>
              {t("settings.mail_password_on_file")}
            </Pill>
            <label>
              <input type="checkbox" checked={clearPassword} onChange={(event) => setClearPassword(event.target.checked)} />
              {t("settings.mail_password_clear")}
            </label>
          </div>
        )}
        <Field label={t("settings.mail_from_label")} htmlFor="mail-from" help={t("settings.mail_from_help")}>
          <Input id="mail-from" mono type="email" value={from} onChange={(event) => setFrom(event.target.value)} maxLength={254} autoComplete="off" />
        </Field>
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
        <div className="cluster">
          <Button type="submit" variant="primary" icon="check" disabled={busy}>
            {t("settings.mail_save")}
          </Button>
          {saved && <Pill tone="ok">{t("settings.mail_saved")}</Pill>}
        </div>
        {settings.configured ? (
          <>
            <Field label={t("settings.mail_test_label")} htmlFor="mail-test-to">
              <Input id="mail-test-to" mono type="email" value={testTo} onChange={(event) => setTestTo(event.target.value)} maxLength={254} autoComplete="email" />
            </Field>
            <div className="cluster">
              <Button icon="refresh" onClick={runTest} disabled={busy || testTo === ""}>
                {t("settings.mail_test")}
              </Button>
              {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{channelTestResult(t, test)}</Pill>}
            </div>
          </>
        ) : (
          <p className="secondary">{t("settings.mail_not_configured")}</p>
        )}
      </form>
    </Card>
  );
}
