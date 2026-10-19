import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

import { api, errorKey, segment } from "../api/client";
import type { AlertSeverity, Channel, ChannelFormat, ChannelTest, MailSettings } from "../api/types";
import { Button } from "../components/Button";
import { Card, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { channelTestResult } from "../lib/alerts";
import { useRefresh } from "../api/refresh";
import { NotFound } from "./NotFound";
import "./StatusAdmin.scss";

const formats: readonly ChannelFormat[] = ["json", "text", "discord", "slack", "email", "matrix"];

// Le secret d'un webhook signe le corps ; celui d'un canal Matrix est le
// jeton du bot. Même champ, mêmes règles, d'autres mots.
const signingSecretKeys = { label: "channel.secret_label", help: "channel.secret_help", keep: "channel.secret_keep", onFile: "channel.secret_on_file" };
const matrixTokenKeys = { label: "channel.matrix_token_label", help: "channel.matrix_token_help", keep: "channel.matrix_token_keep", onFile: "channel.matrix_token_on_file" };

// Créer ou modifier un canal : un nom, une URL, un format, un secret de
// signature facultatif, la gravité minimale, et deux cases. Le secret ne
// revient jamais du serveur : on sait seulement qu'il y en a un. Un canal
// e-mail remplace l'URL et le secret par ses destinataires. Un canal
// Matrix nomme son serveur d'accueil, son salon, et le jeton du bot à la
// place du secret.
export function AlertChannelForm() {
  const { id } = useParams();
  const t = useT();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const justCreated = id !== undefined && params.get("cree") === "1";
  const { refresh } = useRefresh();
  const existing = useResource<Channel>(id !== undefined ? () => `/api/alerts/channels/${segment(id)}` : null);
  const mail = useResource<MailSettings>("/api/settings/mail");
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [destination, setDestination] = useState("");
  const [format, setFormat] = useState<ChannelFormat>("json");
  const [secret, setSecret] = useState("");
  const [clearSecret, setClearSecret] = useState(false);
  const [minSeverity, setMinSeverity] = useState<AlertSeverity>("attention");
  const [notifyResolve, setNotifyResolve] = useState(true);
  const [enabled, setEnabled] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [test, setTest] = useState<ChannelTest | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (existing.data) {
      setName(existing.data.name);
      setURL(existing.data.url);
      setDestination(existing.data.destination ?? "");
      setFormat(existing.data.format);
      setMinSeverity(existing.data.min_severity);
      setNotifyResolve(existing.data.notify_resolve);
      setEnabled(existing.data.enabled);
    }
  }, [existing.data]);

  if (id !== undefined && existing.error?.status === 404) {
    return <NotFound />;
  }

  const isEmail = format === "email";
  const isMatrix = format === "matrix";
  const hasSecret = existing.data?.has_secret ?? false;
  const body = { name, url, format, destination, secret, clear_secret: clearSecret, min_severity: minSeverity, notify_resolve: notifyResolve, enabled };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const request = id === undefined ? api.post<Channel>("/api/alerts/channels", body) : api.put<Channel>(`/api/alerts/channels/${segment(id)}`, body);
    request
      .then((saved) => {
        setError(null);
        refresh("alerts");
        // Un canal neuf n'a encore rien reçu : sa fiche propose de l'essayer.
        void navigate(id === undefined ? `/alertes/canaux/${saved.id}?cree=1` : "/alertes/canaux");
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  const runTest = () => {
    if (id === undefined) {
      return;
    }
    setBusy(true);
    setTest(null);
    api
      .post<ChannelTest>(`/api/alerts/channels/${segment(id)}/actions/test`)
      .then(setTest)
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  const remove = () => {
    if (id === undefined || !existing.data || !window.confirm(t("channel.delete_confirm", existing.data.name))) {
      return;
    }
    api
      .delete(`/api/alerts/channels/${segment(id)}`)
      .then(() => {
        refresh("alerts");
        void navigate("/alertes/canaux");
      })
      .catch((failure: unknown) => setError(errorKey(failure)));
  };

  return (
    <>
      <PageHead title={id === undefined ? t("channel.new_title") : t("channel.edit_title")} subtitle={t("channel.new_subtitle")} />
      {existing.error && <Failure error={existing.error} />}
      {justCreated && <Note tone="ok">{t("channel.created")}</Note>}
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("channel.name_label")} htmlFor="channel-name" help={t("channel.name_help")}>
            <Input id="channel-name" type="text" value={name} onChange={(event) => setName(event.target.value)} maxLength={80} autoComplete="off" autoFocus required />
          </Field>
          <div className="grid-2">
            <Field label={t("channel.format_label")} htmlFor="channel-format" help={t("channel.format_help")}>
              <Select id="channel-format" value={format} onChange={(event) => setFormat(event.target.value as ChannelFormat)}>
                {formats.map((candidate) => (
                  <option key={candidate} value={candidate}>
                    {t(`channel.format_${candidate}`)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("channel.min_severity_label")} htmlFor="channel-severity" help={t("channel.min_severity_help")}>
              <Select id="channel-severity" value={minSeverity} onChange={(event) => setMinSeverity(event.target.value as AlertSeverity)}>
                <option value="attention">{t("alert.severity_attention")}</option>
                <option value="danger">{t("alert.severity_danger")}</option>
              </Select>
            </Field>
          </div>
          {isEmail ? (
            <>
              <Field label={t("channel.recipients_label")} htmlFor="channel-recipients" help={t("channel.recipients_help")}>
                <Input id="channel-recipients" mono type="text" value={destination} onChange={(event) => setDestination(event.target.value)} maxLength={2600} autoComplete="off" required />
              </Field>
              {mail.data !== null && !mail.data.configured && (
                <Note tone="warn">
                  {t("channel.mail_relay_missing")} <Link to="/parametres">{t("channel.mail_relay_link")}</Link>
                </Note>
              )}
            </>
          ) : (
            <>
              <Field label={t(isMatrix ? "channel.matrix_homeserver_label" : "channel.url_label")} htmlFor="channel-url" help={t(isMatrix ? "channel.matrix_homeserver_help" : "channel.url_help")}>
                <Input id="channel-url" mono type="url" value={url} onChange={(event) => setURL(event.target.value)} maxLength={2048} autoComplete="off" required />
              </Field>
              {url.startsWith("http://") && <Note tone="warn">{t("channel.plain_warning")}</Note>}
              {isMatrix && (
                <Field label={t("channel.matrix_room_label")} htmlFor="channel-room" help={t("channel.matrix_room_help")}>
                  <Input id="channel-room" mono type="text" value={destination} onChange={(event) => setDestination(event.target.value)} maxLength={255} placeholder={t("channel.matrix_room_placeholder")} autoComplete="off" required />
                </Field>
              )}
            </>
          )}
          {!isEmail && <SecretField isMatrix={isMatrix} hasSecret={hasSecret} secret={secret} setSecret={setSecret} clearSecret={clearSecret} setClearSecret={setClearSecret} />}
          <div className="checks">
            <label>
              <input type="checkbox" checked={notifyResolve} onChange={(event) => setNotifyResolve(event.target.checked)} />
              {t("channel.notify_resolve_label")}
            </label>
            <label>
              <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
              {t("channel.enabled_label")}
            </label>
          </div>
          <Failure error={error} />
          {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{channelTestResult(t, test)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon={id === undefined ? "plus" : "check"} disabled={busy}>
              {id === undefined ? t("channel.create") : t("channel.save")}
            </Button>
            {id !== undefined && (
              <Button icon="refresh" onClick={runTest} disabled={busy}>
                {t("channel.test")}
              </Button>
            )}
            <Button variant="ghost" to="/alertes/canaux">
              {t("channel.cancel")}
            </Button>
            {id !== undefined && (
              <Button variant="danger" icon="x" onClick={remove} disabled={busy}>
                {t("channel.delete")}
              </Button>
            )}
          </div>
        </form>
      </Card>
    </>
  );
}

// Le secret de signature d'un webhook, ou le jeton d'un bot Matrix :
// jamais réaffiché, gardé quand le champ reste vide, retiré par une case
// sauf pour Matrix.
function SecretField({
  isMatrix,
  hasSecret,
  secret,
  setSecret,
  clearSecret,
  setClearSecret,
}: {
  isMatrix: boolean;
  hasSecret: boolean;
  secret: string;
  setSecret: (secret: string) => void;
  clearSecret: boolean;
  setClearSecret: (clear: boolean) => void;
}) {
  const t = useT();
  const keys = isMatrix ? matrixTokenKeys : signingSecretKeys;
  return (
    <>
      <Field label={t(keys.label)} htmlFor="channel-secret" help={hasSecret ? t(keys.keep) : t(keys.help)}>
        <Input id="channel-secret" mono type="password" value={secret} onChange={(event) => setSecret(event.target.value)} maxLength={256} autoComplete="new-password" required={isMatrix && !hasSecret} />
      </Field>
      {hasSecret && (
        <div className="checks">
          <Pill tone="ok" dot={false}>
            {t(keys.onFile)}
          </Pill>
          {/* Un bot sans jeton ne peut rien écrire : on remplace le jeton, on ne le retire pas. */}
          {!isMatrix && (
            <label>
              <input type="checkbox" checked={clearSecret} onChange={(event) => setClearSecret(event.target.checked)} />
              {t("channel.secret_clear")}
            </label>
          )}
        </div>
      )}
    </>
  );
}
