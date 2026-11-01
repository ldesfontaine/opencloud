import { Navigate, useNavigate, useParams } from "react-router";

import { setMachineProxmox, useMachineProxmox } from "../../api/proxmox";
import { useMachines, type Machine } from "../../api/machines";
import { AddressCheck, CertificateFingerprint } from "../../components/CertificateCheck";
import { Field, Input } from "../../components/Field";
import { Modal } from "../../components/Modal";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useCertificateInspector, type CertificateInspector } from "../../hooks/useCertificateInspector";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import { maskCredential } from "../../lib/credential";
import { displayPin } from "../../lib/fingerprint";
import { isHTTPSAddress } from "../../lib/firewallForm";
import { detectedProxmox, emptyProxmoxForm, isProxmoxFormModified, proxmoxAccessMissing, proxmoxDefinitionOf, proxmoxFormOf, suggestedProxmoxURL, type ProxmoxFormValues } from "../../lib/proxmox";

// « Déclarer l'hôte Proxmox » : la fenêtre de la fiche d'une machine, à
// l'adresse /machines/:id/proxmox, aussi pour modifier un hôte déjà déclaré.
// Deux étapes (l'accès, le certificat) puis le récapitulatif. Le serveur
// interroge l'API avec le jeton avant de rien garder ; un refus reste sur le
// récapitulatif.
export function DeclareProxmoxHost() {
  const { id = "" } = useParams();
  const machines = useMachines();
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (machines.data && !current) {
    return <Navigate to="/machines" replace />;
  }
  return current ? <ProxmoxDialog machine={current} /> : null;
}

function ProxmoxDialog({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const declared = machine.proxmox.declared;
  const host = useMachineProxmox(machine.id, declared);
  // Un hôte reconnu par l'agent arrive avec l'adresse de la machine ; le jeton,
  // lui, reste à l'opérateur.
  const initial = emptyProxmoxForm(detectedProxmox(machine) === null ? "" : suggestedProxmoxURL(machine));
  const form = useForm(initial, declared ? { source: host.data, fill: proxmoxFormOf } : null);
  const { values } = form;
  const action = useAction();
  const inspector = useCertificateInspector(values.apiURL);
  const close = () => void navigate(`/machines/${machine.id}`, { replace: true });
  const save = () => {
    action.run(() => setMachineProxmox(machine.id, proxmoxDefinitionOf(values)), {
      topic: "machines",
      onDone: () => void navigate(`/machines/${machine.id}/hyperviseur`, { replace: true }),
    });
  };
  const base = declared && host.data !== null ? proxmoxFormOf(host.data) : initial;
  return (
    <Modal title={t("machine.proxmox_form_title", machine.name)} onClose={close} dirty={form.isFilled && isProxmoxFormModified(values, base)} wide>
      <p className="secondary">{t("machine.proxmox_form_subtitle")}</p>
      {form.isFilled && <Wizard steps={stepsOf(t, form, inspector, host.data?.token_id_hint ?? null)} mode="create" onSubmit={save} submitLabel={t("machine.proxmox_submit")} submitIcon="check" busy={action.busy} error={action.error} />}
    </Modal>
  );
}

function stepsOf(t: Translate, form: ReturnType<typeof useForm<ProxmoxFormValues, ProxmoxFormValues>>, inspector: CertificateInspector, tokenIDHint: string | null): WizardStep[] {
  const { values } = form;
  return [
    {
      id: "access",
      title: t("machine.proxmox_step_access"),
      render: () => (
        <>
          <Field label={t("machine.proxmox_url_label")} htmlFor="proxmox-url" help={t("machine.proxmox_url_help")}>
            <Input id="proxmox-url" mono value={values.apiURL} placeholder="https://pve.lan:8006" onChange={(event) => form.change("apiURL", event.target.value)} autoComplete="off" autoFocus />
          </Field>
          <AddressCheck inspector={inspector} isAddressValid={isHTTPSAddress(values.apiURL)} />
          <Field label={t("machine.proxmox_token_id_label")} htmlFor="proxmox-token-id" help={tokenIDHint === null ? t("machine.proxmox_token_id_help") : t("machine.proxmox_token_id_current", tokenIDHint)}>
            <Input id="proxmox-token-id" mono value={values.tokenID} placeholder="lecteur@pve!lecture" onChange={(event) => form.change("tokenID", event.target.value)} autoComplete="off" />
          </Field>
          <Field label={t("machine.proxmox_secret_label")} htmlFor="proxmox-secret" help={t("machine.proxmox_secret_help")}>
            <Input id="proxmox-secret" mono type="password" value={values.tokenSecret} onChange={(event) => form.change("tokenSecret", event.target.value)} autoComplete="new-password" />
          </Field>
        </>
      ),
      validate: () => (proxmoxAccessMissing(values) ? "machine.proxmox_access_missing" : null),
      summary: () => [
        { label: t("machine.proxmox_url_label"), value: values.apiURL.trim(), mono: true },
        { label: t("machine.proxmox_token_id_label"), value: maskCredential(values.tokenID.trim()), mono: true },
        { label: t("machine.proxmox_secret_label"), value: t("machine.proxmox_secret_hidden") },
      ],
    },
    {
      id: "certificate",
      title: t("machine.proxmox_step_certificate"),
      render: () => (
        <CertificateFingerprint
          inspector={inspector}
          pin={values.pin}
          onPin={(pin) => form.change("pin", pin)}
          explanation={t("machine.proxmox_certificate_explanation")}
          fieldId="proxmox-pin"
        />
      ),
      summary: () => [{ label: t("machine.proxmox_pin_label"), value: values.pin.trim() === "" ? t("machine.proxmox_system_trust") : displayPin(values.pin), mono: values.pin.trim() !== "" }],
    },
  ];
}
