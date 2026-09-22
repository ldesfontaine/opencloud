import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";

import { api, errorKey } from "../api/client";
import type { AlertSeverity, Channel, ChannelFormat, ChannelTest } from "../api/types";
import { Button } from "../components/Button";
import { Card, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { deliveryReason } from "../lib/alerts";
import { useRefresh } from "../lib/refresh";
import { NotFound } from "./NotFound";
import "./StatusAdmin.scss";

const formats: readonly ChannelFormat[] = ["json", "text", "discord", "slack"];

// Créer ou modifier un canal : un nom, une URL, un format, un secret de
// signature facultatif, la gravité minimale, et deux cases. Le secret ne
// revient jamais du serveur : on sait seulement qu'il y en a un.
export function AlertChannelForm() {
  const { id } = useParams();
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const existing = useResource<Channel>(id !== undefined ? `/api/alerts/channels/${id}` : null);
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
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
      setFormat(existing.data.format);
      setMinSeverity(existing.data.min_severity);
      setNotifyResolve(existing.data.notify_resolve);
      setEnabled(existing.data.enabled);
    }
  }, [existing.data]);

  if (id !== undefined && existing.error?.status === 404) {
    return <NotFound />;
  }

  const body = { name, url, format, secret, clear_secret: clearSecret, min_severity: minSeverity, notify_resolve: notifyResolve, enabled };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const request = id === undefined ? api.post<Channel>("/api/alerts/channels", body) : api.put<Channel>(`/api/alerts/channels/${id}`, body);
    request
      .then(() => {
        refresh("alerts");
        void navigate("/alertes/canaux");
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
      .post<ChannelTest>(`/api/alerts/channels/${id}/actions/test`)
      .then(setTest)
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  const remove = () => {
    if (id === undefined || !existing.data || !window.confirm(t("channel.delete_confirm", existing.data.name))) {
      return;
    }
    api
      .delete(`/api/alerts/channels/${id}`)
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
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("channel.name_label")} htmlFor="channel-name" help={t("channel.name_help")}>
            <Input id="channel-name" type="text" value={name} onChange={(event) => setName(event.target.value)} maxLength={80} autoComplete="off" autoFocus required />
          </Field>
          <Field label={t("channel.url_label")} htmlFor="channel-url" help={t("channel.url_help")}>
            <Input id="channel-url" mono type="url" value={url} onChange={(event) => setURL(event.target.value)} maxLength={2048} autoComplete="off" required />
          </Field>
          {url.startsWith("http://") && <Note tone="warn">{t("channel.plain_warning")}</Note>}
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
          <Field label={t("channel.secret_label")} htmlFor="channel-secret" help={existing.data?.has_secret ? t("channel.secret_keep") : t("channel.secret_help")}>
            <Input id="channel-secret" mono type="password" value={secret} onChange={(event) => setSecret(event.target.value)} maxLength={256} autoComplete="new-password" />
          </Field>
          {existing.data?.has_secret && (
            <div className="checks">
              <Pill tone="ok" dot={false}>
                {t("channel.secret_on_file")}
              </Pill>
              <label>
                <input type="checkbox" checked={clearSecret} onChange={(event) => setClearSecret(event.target.checked)} />
                {t("channel.secret_clear")}
              </label>
            </div>
          )}
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
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{test.ok ? t("channel.test_ok", test.code) : t("channel.test_failed", deliveryReason(t, test))}</Pill>}
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
