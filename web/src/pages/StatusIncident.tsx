import { Outlet, useNavigate, useParams } from "react-router";

import { useProjects } from "../api/directory";
import { addIncidentUpdate, deleteIncident, useIncident, useStatusComponents, type Incident } from "../api/status";
import { Button } from "../components/Button";
import { ConfirmModal } from "../components/ConfirmModal";
import { Card, CardBody, CardHeader, Empty, KeyValue } from "../components/Card";
import { Failure } from "../components/Failure";
import { Loaded } from "../components/Loaded";
import { Field, Textarea } from "../components/Field";
import { FormActions, FormBody } from "../components/FormBody";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { EnumSelect } from "../components/Select";
import { useNow } from "../hooks/useNow";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import {
  componentChoiceLabel,
  editIncidentPath,
  incidentsPath,
  impactTones,
  incidentTones,
  incidentUpdateFormOf,
  incidentUpdateRequestOf,
  saysNothing,
  updateStatusesFor,
} from "../lib/status";
import { ago, formatClock } from "../lib/time";
import "./StatusIncident.scss";

// La page d'un incident : ses faits, son fil, le formulaire pour y
// ajouter une entrée, et la correction du titre, des cibles et de la
// fenêtre.
export function StatusIncidentPage() {
  const { id = "" } = useParams();
  const incident = useIncident(id);
  return <Loaded resource={incident}>{(data) => <IncidentView incident={data} reload={incident.reload} />}</Loaded>;
}

function IncidentView({ incident, reload }: { incident: Incident; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const navigate = useNavigate();
  const resolved = incident.status === "resolved";
  const components = useStatusComponents(null).data?.components ?? [];
  const projects = useProjects().data?.projects ?? [];
  // Les composants que les cibles touchent, avec leur projet dès qu'il y en a plusieurs.
  const shownOn = incident.components.map((touched) => componentChoiceLabel(components.find((component) => component.id === touched.id) ?? { name: touched.name, project_id: "" }, projects));

  const action = useAction();

  const remove = () => {
    action.run(() => deleteIncident(incident.id), {
      topic: "status",
      onDone: () => void navigate(incidentsPath),
    });
  };

  return (
    <>
      <PageHead
        title={incident.title}
        actions={
          <>
            <Button icon="settings" to={editIncidentPath(incident.id)}>
              {t("incident.edit")}
            </Button>
            <ConfirmModal
              title={t("incident.delete")}
              question={t("incident.delete_confirm", incident.title)}
              confirmLabel={t("incident.delete")}
              onConfirm={remove}
              variant="danger"
              icon="x"
            >
              {t("incident.delete")}
            </ConfirmModal>
          </>
        }
      />
      <Failure error={action.error} />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <Pill tone={incidentTones[incident.status]}>{t(`incident.status_${incident.status}`)}</Pill>
            <Pill tone={impactTones[incident.impact]}>{t(`incident.impact_${incident.impact}`)}</Pill>
            <span className="meta">
              <span>{t("incident.opened_at", formatClock(incident.created_at))}</span>
              {incident.resolved_at !== null && (
                <>
                  <span className="sep" />
                  <span>{t("incident.resolved_at", formatClock(incident.resolved_at))}</span>
                </>
              )}
              {incident.starts_at !== null && incident.ends_at !== null && (
                <>
                  <span className="sep" />
                  <span>
                    {t("incident.window")}{" "}
                    <span className="mono">
                      {formatClock(incident.starts_at)} → {formatClock(incident.ends_at)}
                    </span>
                  </span>
                </>
              )}
            </span>
          </span>
          <KeyValue label={t("incident.field_targets")} value={listOrDash(incident.targets.map((target) => target.name))} />
          <KeyValue label={t("incident.field_shown_on")} value={listOrDash(shownOn)} />
        </CardBody>
      </Card>
      <div className="grid-2">
        <Card>
          <CardHeader title={t("incident.timeline_title")} />
          <CardBody>
            {incident.updates.length === 0 ? (
              <Empty text={t("incident.empty_text")} />
            ) : (
              <div className="timeline">
                {incident.updates.map((update, index) => (
                  <div className="entry" key={index}>
                    <span className="medium">{t(`incident.status_${update.status}`)}</span>
                    <span className="secondary nowrap" title={formatClock(update.created_at)}>
                      {ago(t, update.created_at, now)}
                    </span>
                    {update.message !== "" && <span className="entry-message">{update.message}</span>}
                  </div>
                ))}
              </div>
            )}
          </CardBody>
        </Card>
        {!resolved && <UpdateForm incident={incident} reload={reload} />}
      </div>
      {/* La fenêtre de l'adresse : corriger l'incident. */}
      <Outlet />
    </>
  );
}

// Une liste de noms sur une ligne, ou un tiret quand il n'y en a pas.
function listOrDash(names: string[]): string {
  return names.length === 0 ? "—" : names.join(", ");
}

// Une entrée de plus au fil : le statut qu'elle donne, un message ;
// « résolu » clôt l'incident.
function UpdateForm({ incident, reload }: { incident: Incident; reload: () => void }) {
  const t = useT();
  const form = useForm(incidentUpdateFormOf(incident));
  const { values, change } = form;
  const action = useAction();

  const save = () => {
    const body = incidentUpdateRequestOf(values);
    action.run(() => addIncidentUpdate(incident.id, body), {
      topic: "status",
      onDone: () => {
        change("message", "");
        reload();
      },
    });
  };

  return (
    <Card>
      <CardHeader title={t("incident.update_title")} aside={t("incident.update_text")} />
      <FormBody
        onSubmit={save}
        error={action.error}
        actions={
        <FormActions
          submitLabel={values.status === "resolved" ? t("incident.resolve") : t("incident.update_button")}
          submitIcon="check"
          busy={action.busy}
          canSubmit={!saysNothing(values, incident)}
        />
      }
      >
      <Field label={t("incident.status_label")} htmlFor="update-status">
        <EnumSelect id="update-status" value={values.status} values={updateStatusesFor(incident.impact)} labelKey="incident.status_" onChange={(status) => change("status", status)} />
      </Field>
      <Field label={t("incident.message_label")} htmlFor="update-message" help={t("incident.update_help")}>
        <Textarea id="update-message" value={values.message} onChange={(event) => change("message", event.target.value)} maxLength={2000} rows={3} />
      </Field>
      </FormBody>
    </Card>
  );
}
