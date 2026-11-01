import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";

import { useDeclaredService, useDeclaredServices, type DeclaredService, type DeclaredServiceDetail } from "../../api/directory";
import { createLink } from "../../api/links";
import { Field, Input } from "../../components/Field";
import { ModalForm } from "../../components/ModalForm";
import { Select } from "../../components/Select";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { emptyLinkForm, isLinkComplete, linkRequestOf, peerOptions, unitOptions, type LinkDirection, type LinkFormValues } from "../../lib/links";

// « Ajouter un lien » : la fenêtre de la fiche d'un service, à l'adresse
// /services/annuaire/:id/liens/nouveau. Une seule étape : le sens, le
// service en face, et les unités et le port seulement quand ils servent.
export function LinkModal() {
  const { id = "" } = useParams();
  const service = useDeclaredService(id);
  const services = useDeclaredServices();
  if (service.error) {
    return <Navigate to="/services" replace />;
  }
  return service.data && services.data ? <LinkDialog self={service.data} services={services.data.services} /> : null;
}

function LinkDialog({ self, services }: { self: DeclaredServiceDetail; services: DeclaredService[] }) {
  const t = useT();
  const navigate = useNavigate();
  const action = useAction();
  const [values, setValues] = useState<LinkFormValues>(emptyLinkForm());
  const close = () => void navigate(`/services/annuaire/${self.id}`, { replace: true });
  const peer = services.find((candidate) => candidate.id === values.peerServiceID);
  const selfUnits = unitOptions(self);
  const peerUnits = unitOptions(peer);
  const complete = isLinkComplete(values, self, peer);
  const directionOptions: { value: LinkDirection; label: string }[] = [
    { value: "outgoing", label: t("link.direction_outgoing") },
    { value: "incoming", label: t("link.direction_incoming") },
  ];

  const save = () => {
    action.run(() => createLink(linkRequestOf(self.id, values)), { topic: "directory", onDone: close });
  };

  return (
    <ModalForm
      title={t("link.dialog_title")}
      onClose={close}
      dirty={values.peerServiceID !== "" || values.port !== ""}
      onSubmit={save}
      submitLabel={t("link.dialog_save")}
      submitIcon="plus"
      busy={action.busy}
      canSubmit={complete}
      error={action.error}
    >
      <Field label={t("link.field_direction")} htmlFor="link-direction">
        <Select id="link-direction" value={values.direction} options={directionOptions} onChange={(direction) => setValues({ ...values, direction: direction as LinkDirection })} />
      </Field>
      <Field label={t("link.field_peer")} htmlFor="link-peer" help={t("link.field_peer_help")}>
        <Select
          id="link-peer"
          value={values.peerServiceID}
          options={peerOptions(services, self.id)}
          placeholder={t("link.peer_none")}
          onChange={(peerServiceID) => setValues({ ...values, peerServiceID, peerUnitID: "" })}
        />
      </Field>
      {peerUnits.length > 0 && (
        <Field label={t("link.field_peer_unit")} htmlFor="link-peer-unit" help={t("link.field_unit_help")}>
          <Select id="link-peer-unit" value={values.peerUnitID} options={peerUnits} placeholder={t("link.peer_none")} onChange={(peerUnitID) => setValues({ ...values, peerUnitID })} />
        </Field>
      )}
      {selfUnits.length > 0 && (
        <Field label={t("link.field_self_unit")} htmlFor="link-self-unit" help={t("link.field_unit_help")}>
          <Select id="link-self-unit" value={values.selfUnitID} options={selfUnits} placeholder={t("link.peer_none")} onChange={(selfUnitID) => setValues({ ...values, selfUnitID })} />
        </Field>
      )}
      <Field label={t("link.field_port")} htmlFor="link-port" help={t("link.field_port_help")}>
        <Input id="link-port" inputMode="numeric" value={values.port} onChange={(event) => setValues({ ...values, port: event.target.value })} />
      </Field>
    </ModalForm>
  );
}
