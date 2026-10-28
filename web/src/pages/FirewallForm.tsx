import { useNavigate, useParams } from "react-router";

import { declareFirewall, firewallKinds, redeclareFirewall, useFirewall, type FirewallDetail, type FirewallKind } from "../api/firewalls";
import { EnumSelect, Field, Input } from "../components/Field";
import { Failure } from "../components/Failure";
import { FormPage } from "../components/FormPage";
import { PageHead } from "../components/PageHead";
import { Wizard, type WizardStep } from "../components/Wizard";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT, type Translate } from "../i18n/context";
import { isCredential, isHTTPSAddress } from "../lib/firewallForm";
import { NotFound } from "./NotFound";

interface FirewallFormValues {
  name: string;
  kind: FirewallKind;
  apiURL: string;
  apiKey: string;
  apiSecret: string;
  pin: string;
}

const emptyForm: FirewallFormValues = { name: "", kind: "opnsense", apiURL: "", apiKey: "", apiSecret: "", pin: "" };

// Ce que le serveur garde d'un pare-feu déjà déclaré, pour le modifier : jamais
// le secret, qui se ressaisit.
function formOf(firewall: FirewallDetail): FirewallFormValues {
  return { name: firewall.name, kind: firewall.kind, apiURL: firewall.api_url, apiKey: firewall.api_key, apiSecret: "", pin: firewall.pin };
}

// Déclarer (ou modifier) un pare-feu, pas à pas. Le serveur interroge l'API avec
// la paire de clés avant de rien garder ; un refus se dit sur le récapitulatif.
export function FirewallForm() {
  const { id } = useParams();
  if (id === undefined) {
    return <FirewallWizard firewall={null} />;
  }
  return <EditFirewall id={id} />;
}

function EditFirewall({ id }: { id: string }) {
  const firewall = useFirewall(id);
  if (firewall.error?.status === 404) {
    return <NotFound />;
  }
  if (!firewall.data) {
    return firewall.error ? <Failure error={firewall.error} /> : null;
  }
  return <FirewallWizard firewall={firewall.data} />;
}

function FirewallWizard({ firewall }: { firewall: FirewallDetail | null }) {
  const t = useT();
  const navigate = useNavigate();
  const action = useAction();
  const form = useForm(emptyForm, firewall === null ? null : { source: firewall, fill: formOf });
  const { values } = form;
  const isEdit = firewall !== null;
  const save = () => {
    const definition = {
      name: values.name.trim(),
      kind: values.kind,
      api_url: values.apiURL.trim(),
      api_key: values.apiKey.trim(),
      api_secret: values.apiSecret,
      pin: values.pin.trim(),
    };
    action.run(() => (firewall === null ? declareFirewall(definition) : redeclareFirewall(firewall.id, definition)), {
      topic: "firewalls",
      onDone: (saved) => void navigate(`/pare-feu/${saved.id}`),
    });
  };
  const steps = stepsOf(t, form);
  return (
    <FormPage width="wizard">
      <PageHead title={isEdit ? t("firewall.form_title_edit", firewall.name) : t("firewall.form_title_new")} subtitle={t("firewall.form_subtitle")} />
      {form.isFilled && (
        <Wizard
          steps={steps}
          mode={isEdit ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEdit ? t("firewall.submit_edit") : t("firewall.submit_new")}
          submitIcon={isEdit ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
          cancelLabel={t("firewall.cancel")}
          cancelTo={isEdit ? `/pare-feu/${firewall.id}` : "/pare-feu"}
        />
      )}
    </FormPage>
  );
}

function stepsOf(t: Translate, form: ReturnType<typeof useForm<FirewallFormValues, FirewallDetail>>): WizardStep[] {
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
            <EnumSelect id="firewall-kind" value={values.kind} values={firewallKinds} labelKey="firewall.kind_" onChange={(kind) => form.change("kind", kind)} />
          </Field>
        </>
      ),
      validate: () => (values.name.trim() === "" ? "firewall.name_invalid" : null),
      summary: () => [
        { label: t("firewall.name_label"), value: values.name },
        { label: t("firewall.connector_label"), value: t(`firewall.kind_${values.kind}`) },
      ],
    },
    {
      id: "access",
      title: t("firewall.step_access"),
      render: () => (
        <>
          <p className="secondary">{t("firewall.none_howto")}</p>
          <p className="secondary">{t("firewall.rights_text")}</p>
          <p className="secondary">{t("firewall.rights_cost")}</p>
          <Field label={t("firewall.url_label")} htmlFor="firewall-url" help={t("firewall.url_help")}>
            <Input id="firewall-url" mono value={values.apiURL} placeholder="https://192.0.2.1" onChange={(event) => form.change("apiURL", event.target.value)} autoComplete="off" />
          </Field>
          <Field label={t("firewall.key_label")} htmlFor="firewall-key" help={t("firewall.key_help")}>
            <Input id="firewall-key" mono value={values.apiKey} onChange={(event) => form.change("apiKey", event.target.value)} autoComplete="off" />
          </Field>
          <Field label={t("firewall.secret_label")} htmlFor="firewall-secret" help={t("firewall.secret_help")}>
            <Input id="firewall-secret" mono type="password" value={values.apiSecret} onChange={(event) => form.change("apiSecret", event.target.value)} autoComplete="new-password" />
          </Field>
        </>
      ),
      validate: () => {
        if (!isHTTPSAddress(values.apiURL)) {
          return "firewall.url_invalid";
        }
        if (!isCredential(values.apiKey.trim())) {
          return "firewall.key_invalid";
        }
        return isCredential(values.apiSecret) ? null : "firewall.secret_invalid";
      },
      summary: () => [
        { label: t("firewall.url_label"), value: values.apiURL, mono: true },
        { label: t("firewall.key_label"), value: values.apiKey, mono: true },
        { label: t("firewall.secret_label"), value: t("firewall.secret_hidden") },
      ],
    },
    {
      id: "certificate",
      title: t("firewall.step_certificate"),
      render: () => (
        <Field label={t("firewall.pin_label")} htmlFor="firewall-pin" help={t("firewall.pin_help")}>
          <Input id="firewall-pin" mono value={values.pin} onChange={(event) => form.change("pin", event.target.value)} autoComplete="off" />
        </Field>
      ),
      summary: () => [{ label: t("firewall.pin_label"), value: values.pin.trim() === "" ? t("firewall.pin_none") : values.pin.trim(), mono: values.pin.trim() !== "" }],
    },
  ];
}
