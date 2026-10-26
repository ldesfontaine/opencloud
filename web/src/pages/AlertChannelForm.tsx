import { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";

import { deleteChannel, saveChannel, testChannel, useChannel, type ChannelTest } from "../api/alerts";
import { useMailSettings } from "../api/settings";
import { Button } from "../components/Button";
import { Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Checkbox, Checks, Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { EnumSelect } from "../components/Select";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import { alertSeverities, channelFormats, channelFormOf, channelRequestOf, channelTestResult, emptyChannelForm } from "../lib/alerts";
import { NotFound } from "./NotFound";

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
  const existing = useChannel(id ?? null);
  const mail = useMailSettings();
  const form = useForm(emptyChannelForm, id !== undefined ? { source: existing.data, fill: channelFormOf } : null);
  const { values, change } = form;
  const action = useAction();
  const [test, setTest] = useState<ChannelTest | null>(null);

  if (id !== undefined && existing.error?.status === 404) {
    return <NotFound />;
  }

  const isEmail = values.format === "email";
  const isMatrix = values.format === "matrix";
  const hasSecret = existing.data?.has_secret ?? false;

  const save = () => {
    const body = channelRequestOf(values);
    action.run(() => saveChannel(id, body), {
      topic: "alerts",
      // Un canal neuf n'a encore rien reçu : sa fiche propose de l'essayer.
      onDone: (saved) => void navigate(id === undefined ? `/alertes/canaux/${saved.id}?cree=1` : "/alertes/canaux"),
    });
  };

  const runTest = () => {
    if (id === undefined) {
      return;
    }
    setTest(null);
    action.run(() => testChannel(id), { onDone: setTest });
  };

  const remove = () => {
    if (id === undefined || !existing.data) {
      return;
    }
    action.run(() => deleteChannel(id), {
      confirm: t("channel.delete_confirm", existing.data.name),
      topic: "alerts",
      onDone: () => void navigate("/alertes/canaux"),
    });
  };

  return (
    <FormPage>
      <PageHead title={id === undefined ? t("channel.new_title") : t("channel.edit_title")} subtitle={t("channel.new_subtitle")} />
      {existing.error && <Failure error={existing.error} />}
      {justCreated && <Note tone="ok">{t("channel.created")}</Note>}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <>
            {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{channelTestResult(t, test)}</Pill>}
            <FormActions
              submitLabel={id === undefined ? t("channel.create") : t("channel.save")}
              submitIcon={id === undefined ? "plus" : "check"}
              busy={action.busy}
              canSubmit={form.isFilled}
              cancelLabel={t("channel.cancel")}
              cancelTo="/alertes/canaux"
              deleteLabel={t("channel.delete")}
              onDelete={id !== undefined ? remove : undefined}
            >
              {id !== undefined && (
                <Button icon="refresh" onClick={runTest} disabled={action.busy}>
                  {t("channel.test")}
                </Button>
              )}
            </FormActions>
          </>
        }
      >
        <Field label={t("channel.name_label")} htmlFor="channel-name" help={t("channel.name_help")}>
          <Input id="channel-name" type="text" value={values.name} onChange={(event) => change("name", event.target.value)} maxLength={80} autoComplete="off" autoFocus required />
        </Field>
        <div className="grid-2">
          <Field label={t("channel.format_label")} htmlFor="channel-format" help={t("channel.format_help")}>
            <EnumSelect id="channel-format" value={values.format} values={channelFormats} labelKey="channel.format_" onChange={(format) => change("format", format)} />
          </Field>
          <Field label={t("channel.min_severity_label")} htmlFor="channel-severity" help={t("channel.min_severity_help")}>
            <EnumSelect id="channel-severity" value={values.minSeverity} values={alertSeverities} labelKey="alert.severity_" onChange={(severity) => change("minSeverity", severity)} />
          </Field>
        </div>
        {isEmail ? (
          <>
            <Field label={t("channel.recipients_label")} htmlFor="channel-recipients" help={t("channel.recipients_help")}>
              <Input id="channel-recipients" mono type="text" value={values.destination} onChange={(event) => change("destination", event.target.value)} maxLength={2600} autoComplete="off" required />
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
              <Input id="channel-url" mono type="url" value={values.url} onChange={(event) => change("url", event.target.value)} maxLength={2048} autoComplete="off" required />
            </Field>
            {values.url.startsWith("http://") && <Note tone="warn">{t("channel.plain_warning")}</Note>}
            {isMatrix && (
              <Field label={t("channel.matrix_room_label")} htmlFor="channel-room" help={t("channel.matrix_room_help")}>
                <Input
                  id="channel-room"
                  mono
                  type="text"
                  value={values.destination}
                  onChange={(event) => change("destination", event.target.value)}
                  maxLength={255}
                  placeholder={t("channel.matrix_room_placeholder")}
                  autoComplete="off"
                  required
                />
              </Field>
            )}
          </>
        )}
        {!isEmail && (
          <SecretField
            isMatrix={isMatrix}
            hasSecret={hasSecret}
            secret={values.secret}
            setSecret={(secret) => change("secret", secret)}
            clearSecret={values.clearSecret}
            setClearSecret={(clear) => change("clearSecret", clear)}
          />
        )}
        <Checks>
          <Checkbox label={t("channel.notify_resolve_label")} checked={values.notifyResolve} onChange={(checked) => change("notifyResolve", checked)} />
          <Checkbox label={t("channel.receives_audit_label")} checked={values.receivesAudit} onChange={(checked) => change("receivesAudit", checked)} />
          <Checkbox label={t("channel.enabled_label")} checked={values.enabled} onChange={(checked) => change("enabled", checked)} />
        </Checks>
        <p className="field-help">{t("channel.receives_audit_help")}</p>
      </FormCard>
    </FormPage>
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
        <Checks>
          <Pill tone="ok" dot={false}>
            {t(keys.onFile)}
          </Pill>
          {/* Un bot sans jeton ne peut rien écrire : on remplace le jeton, on ne le retire pas. */}
          {!isMatrix && <Checkbox label={t("channel.secret_clear")} checked={clearSecret} onChange={setClearSecret} />}
        </Checks>
      )}
    </>
  );
}
