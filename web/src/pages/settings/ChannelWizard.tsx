import { useNavigate, useParams } from "react-router";

import { saveChannel, useChannel, type Channel } from "../../api/alerts";
import { useMailSettings } from "../../api/settings";
import { Note } from "../../components/Card";
import { SettingsFold } from "../../components/Disclosure";
import { Checkbox, Checks, Field, Input } from "../../components/Field";
import { FieldTip } from "../../components/FieldTip";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { Modal } from "../../components/Modal";
import { EnumSelect } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { alertCategories, alertSeverities, channelFormats, channelFormOf, channelRequestOf, channelSettingsHint, emptyChannelForm } from "../../lib/alerts";
import { isFormModified } from "../../lib/modal";

// Le secret d'un webhook signe le corps ; celui d'un canal Matrix est le
// jeton du bot. Même champ, mêmes règles, d'autres mots.
const signingSecretKeys = { label: "channel.secret_label", help: "channel.secret_help", keep: "channel.secret_keep", onFile: "channel.secret_on_file" };
const matrixTokenKeys = { label: "channel.matrix_token_label", help: "channel.matrix_token_help", keep: "channel.matrix_token_keep", onFile: "channel.matrix_token_on_file" };

const settingsPath = "/parametres";

// Ajouter ou modifier un canal : la fenêtre de Paramètres › Notifications, à
// /parametres/canaux/nouveau ou /parametres/canaux/:id. Un seul écran : où
// partent les alertes (nom, format, adresse, secret), puis Créer. Lesquelles et
// quand (gravité minimale, résolution, journal, activation) sont repliés sous
// « Réglages ». Le secret ne revient jamais du serveur : on sait seulement
// qu'il y en a un ; vide, le champ garde l'ancien. Un canal e-mail remplace
// l'adresse et le secret par ses destinataires ; un canal Matrix nomme son
// serveur d'accueil, son salon et le jeton du bot.
export function ChannelWizard() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const existing = useChannel(id ?? null);
  const mail = useMailSettings();
  const form = useForm(emptyChannelForm, isEditing ? { source: existing.data, fill: channelFormOf } : null);
  const { values } = form;
  const action = useAction();

  const close = () => void navigate(settingsPath, { replace: true });
  const save = () => {
    const body = channelRequestOf(values);
    action.run(() => saveChannel(id, body), { topic: "alerts", onDone: close });
  };

  const base = existing.data !== null ? channelFormOf(existing.data) : emptyChannelForm;
  return (
    <Modal title={isEditing ? t("channel.edit_title") : t("channel.new_title")} onClose={close} dirty={form.isFilled && isFormModified(values, base)} wide>
      <p className="secondary">{t("channel.new_subtitle")}</p>
      {existing.error && <Failure error={existing.error} />}
      {form.isFilled && (
        <Wizard
          steps={stepsOf(t, form, existing.data, mail.data !== null && !mail.data.configured)}
          mode={isEditing ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEditing ? t("channel.save") : t("channel.create")}
          submitIcon={isEditing ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
        />
      )}
    </Modal>
  );
}

function stepsOf(t: Translate, form: ReturnType<typeof useForm<ReturnType<typeof channelFormOf>, Channel>>, existing: Channel | null, mailMissing: boolean): WizardStep[] {
  const { values, change } = form;
  const isEmail = values.format === "email";
  const isMatrix = values.format === "matrix";
  const hasSecret = existing?.has_secret ?? false;
  return [
    {
      id: "destination",
      title: t("channel.step_destination"),
      render: () => (
        <>
          <Field label={t("channel.name_label")} htmlFor="channel-name" help={t("channel.name_help")}>
            <Input id="channel-name" type="text" value={values.name} onChange={(event) => change("name", event.target.value)} maxLength={80} autoComplete="off" autoFocus required />
          </Field>
          <Field label={t("channel.format_label")} htmlFor="channel-format" help={t("channel.format_help")} tip={t("channel.format_tip")}>
            <EnumSelect id="channel-format" value={values.format} values={channelFormats} labelKey="channel.format_" onChange={(format) => change("format", format)} />
          </Field>
          {isEmail ? (
            <>
              <Field label={t("channel.recipients_label")} htmlFor="channel-recipients" help={t("channel.recipients_help")}>
                <Input id="channel-recipients" mono type="text" value={values.destination} onChange={(event) => change("destination", event.target.value)} maxLength={2600} autoComplete="off" required />
              </Field>
              {mailMissing && (
                <Note tone="warn">{t("channel.mail_relay_missing")}</Note>
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
              <SecretField
                isMatrix={isMatrix}
                hasSecret={hasSecret}
                secret={values.secret}
                setSecret={(secret) => change("secret", secret)}
                clearSecret={values.clearSecret}
                setClearSecret={(clear) => change("clearSecret", clear)}
              />
            </>
          )}
          <SettingsFold hint={channelSettingsHint(t, values)}>
            <Field label={t("channel.min_severity_label")} htmlFor="channel-severity" help={t("channel.min_severity_help")}>
              <EnumSelect id="channel-severity" value={values.minSeverity} values={alertSeverities} labelKey="alert.severity_" onChange={(severity) => change("minSeverity", severity)} />
            </Field>
            <Field label={t("channel.categories_label")} help={t("channel.categories_help")}>
              <Checks>
                {alertCategories.map((category) => (
                  <Checkbox
                    key={category}
                    label={t(`channel.category_${category}`)}
                    checked={values.categories.includes(category)}
                    onChange={(checked) => change("categories", alertCategories.filter((candidate) => (candidate === category ? checked : values.categories.includes(candidate))))}
                  />
                ))}
              </Checks>
            </Field>
            <Checks>
              <Checkbox label={t("channel.notify_resolve_label")} checked={values.notifyResolve} onChange={(checked) => change("notifyResolve", checked)} />
              <Checkbox label={t("channel.receives_audit_label")} checked={values.receivesAudit} onChange={(checked) => change("receivesAudit", checked)} />
              <Checkbox label={t("channel.attach_logs_label")} checked={values.attachLogs} onChange={(checked) => change("attachLogs", checked)} />
              <Checkbox label={t("channel.enabled_label")} checked={values.enabled} onChange={(checked) => change("enabled", checked)} />
            </Checks>
            <p className="field-help">{t("channel.receives_audit_help")}</p>
            <p className="field-help">
              {t("channel.attach_logs_help")} <FieldTip text={t("channel.attach_logs_tip")} />
            </p>
          </SettingsFold>
        </>
      ),
    },
  ];
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
