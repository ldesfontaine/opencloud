import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { editIncident, useIncident, type Incident } from "../../api/status";
import { Field, Input } from "../../components/Field";
import { ModalForm } from "../../components/ModalForm";
import { ObjectPicker } from "../../components/ObjectPicker";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { isFormModified } from "../../lib/modal";
import { incidentEditFormOf, incidentEditRequestOf, incidentPath } from "../../lib/status";

// Corriger ce qui se corrige : la fenêtre de la fiche d'un incident, à
// /disponibilite/incidents/:id/modifier. Le titre, les cibles, la fenêtre
// d'une maintenance.
export function IncidentEditModal() {
  const { id = "" } = useParams();
  const incident = useIncident(id);
  // La fiche derrière montre déjà un échec de lecture : la fenêtre attend l'incident.
  return incident.data === null ? null : <EditDialog incident={incident.data} />;
}

function EditDialog({ incident }: { incident: Incident }) {
  const t = useT();
  const navigate = useNavigate();
  // La saisie se compare à l'incident tel qu'il était à l'ouverture, pas à celui que le direct relit.
  const [initial] = useState(() => incidentEditFormOf(incident));
  const form = useForm(initial);
  const { values, change } = form;
  const action = useAction();
  const maintenance = incident.impact === "maintenance";
  const known = new Map(incident.targets.map((target) => [`${target.kind}:${target.id}`, target.name]));

  const close = () => void navigate(incidentPath(incident.id), { replace: true });
  const save = () => {
    action.run(() => editIncident(incident.id, incidentEditRequestOf(values, incident.impact)), { topic: "status", onDone: close });
  };

  return (
    <ModalForm
      title={t("incident.edit_title")}
      onClose={close}
      dirty={isFormModified(values, initial)}
      wide
      onSubmit={save}
      submitLabel={t("incident.save")}
      submitIcon="check"
      busy={action.busy}
      canSubmit={values.targets.length > 0}
      error={action.error}
    >
      <Field label={t("incident.title_label")} htmlFor="edit-title">
        <Input id="edit-title" type="text" value={values.title} onChange={(event) => change("title", event.target.value)} maxLength={120} required />
      </Field>
      {maintenance && (
        <div className="grid-2">
          <Field label={t("incident.starts_label")} htmlFor="edit-starts">
            <Input id="edit-starts" mono type="datetime-local" value={values.startsAt} onChange={(event) => change("startsAt", event.target.value)} required />
          </Field>
          <Field label={t("incident.ends_label")} htmlFor="edit-ends">
            <Input id="edit-ends" mono type="datetime-local" value={values.endsAt} onChange={(event) => change("endsAt", event.target.value)} required />
          </Field>
        </div>
      )}
      <Field label={t("incident.targets_label")} help={t("incident.targets_help")}>
        <ObjectPicker
          value={values.targets}
          onChange={(targets) => change("targets", targets)}
          labelKeyPrefix="component.kind_"
          label={t("incident.targets_label")}
          addLabel={t("incident.add_target")}
          nameOf={(choice) => known.get(`${choice.kind}:${choice.id}`) ?? t("component.missing")}
        />
      </Field>
    </ModalForm>
  );
}
