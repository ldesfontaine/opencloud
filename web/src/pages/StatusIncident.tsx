import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { addIncidentUpdate, deleteIncident, editIncident, useIncident, useStatusComponents, useStatusPages, type Incident } from "../api/status";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty } from "../components/Card";
import { Failure } from "../components/Failure";
import { Loaded } from "../components/Loaded";
import { Checkbox, Checks, EnumSelect, Field, Input, Textarea } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useNow } from "../hooks/useNow";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT } from "../i18n/context";
import {
  componentChoiceLabel,
  impactTones,
  incidentEditFormOf,
  incidentEditRequestOf,
  incidentTones,
  incidentUpdateFormOf,
  incidentUpdateRequestOf,
  saysNothing,
  toggleComponent,
  updateStatusesFor,
} from "../lib/status";
import { ago, formatClock } from "../lib/time";
import "./StatusAdmin.scss";

// La page d'un incident : ses faits, son fil, le formulaire pour y
// ajouter une entrée, et la correction du titre, des composants et de
// la fenêtre.
export function StatusIncidentPage() {
  const { id = "" } = useParams();
  const incident = useIncident(id);
  return <Loaded resource={incident}>{(data) => <IncidentView incident={data} reload={incident.reload} />}</Loaded>;
}

function IncidentView({ incident, reload }: { incident: Incident; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const navigate = useNavigate();
  const [editing, setEditing] = useState(false);
  const resolved = incident.status === "resolved";

  const action = useAction();

  const remove = () => {
    action.run(() => deleteIncident(incident.id), {
      confirm: t("incident.delete_confirm", incident.title),
      topic: "status",
      onDone: () => void navigate("/page-statut/incidents"),
    });
  };

  return (
    <>
      <PageHead
        title={incident.title}
        actions={
          <>
            {!editing && (
              <Button icon="settings" onClick={() => setEditing(true)}>
                {t("incident.edit")}
              </Button>
            )}
            <Button variant="danger" icon="x" onClick={remove}>
              {t("incident.delete")}
            </Button>
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
              <span>{incident.components.map((component) => component.name).join(", ")}</span>
              <span className="sep" />
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
        </CardBody>
      </Card>
      {editing && (
        <EditForm
          incident={incident}
          done={() => {
            setEditing(false);
            reload();
          }}
          cancel={() => setEditing(false)}
        />
      )}
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
    </>
  );
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
    <FormCard
      onSubmit={save}
      error={action.error}
      header={<CardHeader title={t("incident.update_title")} aside={t("incident.update_text")} />}
      width="fill"
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
    </FormCard>
  );
}

// Corriger ce qui se corrige : le titre, les composants, la fenêtre.
function EditForm({ incident, done, cancel }: { incident: Incident; done: () => void; cancel: () => void }) {
  const t = useT();
  const components = useStatusComponents(null);
  const pages = useStatusPages().data?.pages ?? [];
  const form = useForm(incidentEditFormOf(incident));
  const { values, change } = form;
  const action = useAction();
  const maintenance = incident.impact === "maintenance";

  const save = () => {
    const body = incidentEditRequestOf(values, incident.impact);
    action.run(() => editIncident(incident.id, body), { topic: "status", onDone: done });
  };

  return (
    <FormCard
      onSubmit={save}
      error={action.error}
      header={<CardHeader title={t("incident.edit_title")} />}
      actions={
        <FormActions
          submitLabel={t("incident.save")}
          submitIcon="check"
          busy={action.busy}
          canSubmit={values.componentIDs.length > 0}
          cancelLabel={t("incident.cancel")}
          onCancel={cancel}
        />
      }
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
      <Field label={t("incident.components_label")} help={t("incident.components_help")}>
        <Checks>
          {(components.data?.components ?? []).map((component) => (
            <Checkbox
              key={component.id}
              label={componentChoiceLabel(component, pages)}
              checked={values.componentIDs.includes(component.id)}
              onChange={() => change("componentIDs", toggleComponent(values.componentIDs, component.id))}
            />
          ))}
        </Checks>
      </Field>
    </FormCard>
  );
}
