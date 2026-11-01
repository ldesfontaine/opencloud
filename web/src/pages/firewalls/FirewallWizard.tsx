import { useNavigate, useParams } from "react-router";

import { declareFirewall, firewallKinds, redeclareFirewall, useFirewall, type FirewallDetail, type FirewallKind } from "../../api/firewalls";
import { CertificateFingerprint, AddressCheck } from "../../components/CertificateCheck";
import { Checklist, type ChecklistItem } from "../../components/Checklist";
import { Note } from "../../components/Card";
import { CopyList } from "../../components/Copy";
import { ExternalLink } from "../../components/ExternalLink";
import { Field, Input } from "../../components/Field";
import { Failure } from "../../components/Failure";
import { Modal } from "../../components/Modal";
import { Select } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useCertificateInspector, type CertificateInspector } from "../../hooks/useCertificateInspector";
import { useChecklist } from "../../hooks/useChecklist";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { credentialText, hintText } from "../../lib/credential";
import { displayPin } from "../../lib/fingerprint";
import { isCredential, isHTTPSAddress } from "../../lib/firewallForm";
import { isFormModified } from "../../lib/modal";
import { opnsenseDhcpPrivileges, opnsenseLink, opnsenseReadPrivileges, opnsenseUsersPath } from "../../lib/opnsense";

interface FirewallFormValues {
  name: string;
  kind: FirewallKind;
  apiURL: string;
  apiKey: string;
  apiSecret: string;
  pin: string;
}

const emptyForm: FirewallFormValues = { name: "", kind: "opnsense", apiURL: "", apiKey: "", apiSecret: "", pin: "" };

// Ce que le serveur garde d'un pare-feu déjà déclaré, pour le modifier : ni le
// secret ni la clé en entier, qui se ressaisissent ensemble.
function formOf(firewall: FirewallDetail): FirewallFormValues {
  return { name: firewall.name, kind: firewall.kind, apiURL: firewall.api_url, apiKey: "", apiSecret: "", pin: firewall.pin };
}

// « Déclarer un pare-feu » : la fenêtre de la liste, à /pare-feu/nouveau, ou de
// la fiche, à /pare-feu/:id/modifier. Quatre étapes (identité et adresse,
// préparation d'OPNsense, clé et secret, certificat) et le récapitulatif. Le
// serveur relève lui-même le certificat de l'adresse (« Tester l'accès », puis
// l'empreinte à confirmer) et interroge l'API avec la paire de clés avant de
// rien garder ; un refus se dit sur le récapitulatif.
export function FirewallWizard() {
  const t = useT();
  const navigate = useNavigate();
  const { id } = useParams();
  const isEditing = id !== undefined;
  const existing = useFirewall(id ?? null);
  const form = useForm(emptyForm, isEditing ? { source: existing.data, fill: formOf } : null);
  const { values } = form;
  const action = useAction();
  // Les gestes déjà faits dans OPNsense : un repère, rien ne l'exige.
  const gestures = useChecklist();
  const inspector = useCertificateInspector(values.apiURL);

  const close = () => void navigate(isEditing ? `/pare-feu/${id}` : "/pare-feu", { replace: true });
  const save = () => {
    const definition = {
      name: values.name.trim(),
      kind: values.kind,
      api_url: values.apiURL.trim(),
      api_key: values.apiKey.trim(),
      api_secret: values.apiSecret,
      pin: values.pin.trim(),
    };
    action.run(() => (id === undefined ? declareFirewall(definition) : redeclareFirewall(id, definition)), {
      topic: "firewalls",
      onDone: (saved) => void navigate(`/pare-feu/${saved.id}`, { replace: true }),
    });
  };

  const base = existing.data === null ? emptyForm : formOf(existing.data);
  return (
    <Modal
      title={existing.data === null ? t("firewall.form_title_new") : t("firewall.form_title_edit", existing.data.name)}
      onClose={close}
      dirty={form.isFilled && isFormModified(values, base)}
      wide
    >
      <p className="secondary">{t("firewall.form_subtitle")}</p>
      <Failure error={existing.error} />
      {form.isFilled && (
        <Wizard
          steps={stepsOf(t, form, gestures, inspector, existing.data?.api_key_hint ?? null)}
          mode={isEditing ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEditing ? t("firewall.submit_edit") : t("firewall.submit_new")}
          submitIcon={isEditing ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
        />
      )}
    </Modal>
  );
}

// Les trois gestes à faire dans OPNsense, chacun avec le lien direct vers sa
// page, construit depuis l'adresse saisie à l'étape d'avant. Les libellés de
// menu et de champ sont ceux d'OPNsense dans la langue de l'interface ; les
// privilèges, les noms du champ, que le logiciel ne traduit pas.
function gesturesOf(t: Translate, apiURL: string): ChecklistItem[] {
  const usersLink = opnsenseLink(apiURL, opnsenseUsersPath);
  const openUsers = usersLink !== null && <ExternalLink href={usersLink}>{t("firewall.gesture_open", t("firewall.menu_users"))}</ExternalLink>;
  return [
    {
      id: "user",
      title: t("firewall.gesture_user_title"),
      content: (
        <>
          <p>{t("firewall.gesture_user_text", t("firewall.label_username"), t("firewall.label_password"))}</p>
          {openUsers}
        </>
      ),
    },
    {
      id: "privileges",
      title: t("firewall.gesture_privileges_title"),
      content: (
        <>
          <p>{t("firewall.gesture_privileges_text", t("firewall.label_privileges"))}</p>
          <CopyList values={opnsenseReadPrivileges} />
          <p>{t("firewall.gesture_leases_text")}</p>
          <CopyList values={opnsenseDhcpPrivileges} />
          {openUsers}
        </>
      ),
    },
    {
      id: "key",
      title: t("firewall.gesture_key_title"),
      content: (
        <>
          <p>{t("firewall.gesture_key_text", t("firewall.button_create_key"), t("firewall.answer_yes"), t("firewall.tab_api_keys"))}</p>
          {openUsers}
        </>
      ),
    },
  ];
}

function stepsOf(
  t: Translate,
  form: ReturnType<typeof useForm<FirewallFormValues, FirewallDetail>>,
  gestures: ReturnType<typeof useChecklist>,
  inspector: CertificateInspector,
  keyHint: string | null,
): WizardStep[] {
  const { values } = form;
  return [
    {
      id: "identity",
      title: t("firewall.step_identity"),
      render: () => (
        <>
          <Field label={t("firewall.name_label")} htmlFor="firewall-name" help={t("firewall.name_help")}>
            <Input id="firewall-name" value={values.name} maxLength={80} onChange={(event) => form.change("name", event.target.value)} autoComplete="off" />
          </Field>
          <Field label={t("firewall.connector_label")} htmlFor="firewall-kind" help={t("firewall.connector_help")}>
            <Select
              id="firewall-kind"
              value={values.kind}
              options={firewallKinds.map((kind) => ({ value: kind, label: t(`firewall.kind_${kind}`) }))}
              onChange={(chosen) => {
                const kind = firewallKinds.find((candidate) => candidate === chosen);
                if (kind !== undefined) {
                  form.change("kind", kind);
                }
              }}
            />
          </Field>
          <Field label={t("firewall.url_label")} htmlFor="firewall-url" help={t("firewall.url_help")}>
            <Input id="firewall-url" mono value={values.apiURL} placeholder="https://192.0.2.1" onChange={(event) => form.change("apiURL", event.target.value)} autoComplete="off" />
          </Field>
          <AddressCheck inspector={inspector} isAddressValid={isHTTPSAddress(values.apiURL)} />
        </>
      ),
      validate: () => {
        if (values.name.trim() === "") {
          return "firewall.name_invalid";
        }
        return isHTTPSAddress(values.apiURL) ? null : "firewall.url_invalid";
      },
      summary: () => [
        { label: t("firewall.name_label"), value: values.name },
        { label: t("firewall.connector_label"), value: t(`firewall.kind_${values.kind}`) },
        { label: t("firewall.url_label"), value: values.apiURL.trim(), mono: true },
      ],
    },
    {
      id: "prepare",
      title: t("firewall.step_prepare"),
      render: () => (
        <>
          <p className="secondary">{t("firewall.prepare_intro")}</p>
          <Note tone="warn">{t("firewall.prepare_warning")}</Note>
          <Checklist items={gesturesOf(t, values.apiURL)} done={gestures.done} onToggle={gestures.toggle} />
        </>
      ),
      summary: () => [],
    },
    {
      id: "access",
      title: t("firewall.step_access"),
      render: () => (
        <>
          <Field label={t("firewall.key_label")} htmlFor="firewall-key" help={keyHint === null ? t("firewall.key_help") : t("firewall.key_current", hintText(t, keyHint))}>
            <Input id="firewall-key" mono value={values.apiKey} onChange={(event) => form.change("apiKey", event.target.value)} autoComplete="off" />
          </Field>
          <Field label={t("firewall.secret_label")} htmlFor="firewall-secret" help={t("firewall.secret_help")}>
            <Input id="firewall-secret" mono type="password" value={values.apiSecret} onChange={(event) => form.change("apiSecret", event.target.value)} autoComplete="new-password" />
          </Field>
        </>
      ),
      validate: () => {
        if (!isCredential(values.apiKey.trim())) {
          return "firewall.key_invalid";
        }
        return isCredential(values.apiSecret) ? null : "firewall.secret_invalid";
      },
      summary: () => [
        { label: t("firewall.key_label"), value: credentialText(t, values.apiKey), mono: true },
        { label: t("firewall.secret_label"), value: t("firewall.secret_hidden") },
      ],
    },
    {
      id: "certificate",
      title: t("firewall.step_certificate"),
      render: () => (
        <CertificateFingerprint
          inspector={inspector}
          pin={values.pin}
          onPin={(pin) => form.change("pin", pin)}
          explanation={t("firewall.certificate_explanation", t("firewall.menu_certificates"))}
          fieldId="firewall-pin"
        />
      ),
      summary: () => [{ label: t("firewall.pin_label"), value: values.pin.trim() === "" ? t("firewall.pin_none") : displayPin(values.pin), mono: values.pin.trim() !== "" }],
    },
  ];
}
