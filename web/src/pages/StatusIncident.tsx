import { useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";

import { api, ApiError, errorKey } from "../api/client";
import type { Incident, IncidentStatus, StatusComponentsResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { fromLocalInput, impactTones, incidentTones, statusesFor, toLocalInput } from "../lib/status";
import { ago, formatClock } from "../lib/time";
import { NotFound } from "./NotFound";
import "./StatusAdmin.scss";

// La page d'un incident : ses faits, son fil, le formulaire pour y
// ajouter une entrée, et la correction du titre, des composants et de
// la fenêtre.
export function StatusIncidentPage() {
  const { id = "" } = useParams();
  const incident = useResource<Incident>(`/api/status/incidents/${id}`);
  if (incident.error instanceof ApiError && incident.error.status === 404) {
    return <NotFound />;
  }
  if (!incident.data) {
    return incident.error ? <Failure error={incident.error} /> : null;
  }
  return <IncidentView incident={incident.data} reload={incident.reload} />;
}

function IncidentView({ incident, reload }: { incident: Incident; reload: () => void }) {
  const t = useT();
  const now = useNow();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [editing, setEditing] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const resolved = incident.status === "resolved";

  const remove = () => {
    if (!window.confirm(t("incident.delete_confirm", incident.title))) {
      return;
    }
    api
      .delete(`/api/status/incidents/${incident.id}`)
      .then(() => {
        refresh("status");
        void navigate("/page-statut/incidents");
      })
      .catch(setError);
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
      {error !== null && <Failure error={error} />}
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
            refresh("status");
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
        {!resolved && (
          <UpdateForm
            incident={incident}
            done={() => {
              reload();
              refresh("status");
            }}
          />
        )}
      </div>
    </>
  );
}

// Une entrée de plus au fil : le statut qu'elle donne, un message ;
// « résolu » clôt l'incident.
function UpdateForm({ incident, done }: { incident: Incident; done: () => void }) {
  const t = useT();
  const choices: IncidentStatus[] = statusesFor(incident.impact).filter((candidate) => candidate !== "scheduled");
  const [status, setStatus] = useState<IncidentStatus>(choices.includes(incident.status) ? incident.status : choices[0]!);
  const [message, setMessage] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<Incident>(`/api/status/incidents/${incident.id}/updates`, { status, message })
      .then(() => {
        setMessage("");
        setError(null);
        done();
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <Card>
      <CardHeader title={t("incident.update_title")} aside={t("incident.update_text")} />
      <form className="card-b" onSubmit={submit}>
        <Field label={t("incident.status_label")} htmlFor="update-status">
          <Select id="update-status" value={status} onChange={(event) => setStatus(event.target.value as IncidentStatus)}>
            {choices.map((candidate) => (
              <option key={candidate} value={candidate}>
                {t(`incident.status_${candidate}`)}
              </option>
            ))}
          </Select>
        </Field>
        <Field label={t("incident.message_label")} htmlFor="update-message">
          <textarea id="update-message" className="input textarea" value={message} onChange={(event) => setMessage(event.target.value)} maxLength={2000} rows={3} />
        </Field>
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
        <div className="cluster">
          <Button type="submit" variant="primary" icon="check" disabled={busy}>
            {status === "resolved" ? t("incident.resolve") : t("incident.update_button")}
          </Button>
        </div>
      </form>
    </Card>
  );
}

// Corriger ce qui se corrige : le titre, les composants, la fenêtre.
function EditForm({ incident, done, cancel }: { incident: Incident; done: () => void; cancel: () => void }) {
  const t = useT();
  const components = useResource<StatusComponentsResponse>("/api/status/components");
  const [title, setTitle] = useState(incident.title);
  const [selected, setSelected] = useState(incident.components.map((component) => component.id));
  const [startsAt, setStartsAt] = useState(toLocalInput(incident.starts_at));
  const [endsAt, setEndsAt] = useState(toLocalInput(incident.ends_at));
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const maintenance = incident.impact === "maintenance";

  const toggle = (id: string) => {
    setSelected((current) => (current.includes(id) ? current.filter((candidate) => candidate !== id) : [...current, id]));
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .put<Incident>(`/api/status/incidents/${incident.id}`, {
        title,
        component_ids: selected,
        starts_at: maintenance ? fromLocalInput(startsAt) : null,
        ends_at: maintenance ? fromLocalInput(endsAt) : null,
      })
      .then(done)
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <Card className="form-card">
      <CardHeader title={t("incident.edit_title")} />
      <form className="card-b" onSubmit={submit}>
        <Field label={t("incident.title_label")} htmlFor="edit-title">
          <Input id="edit-title" type="text" value={title} onChange={(event) => setTitle(event.target.value)} maxLength={120} required />
        </Field>
        {maintenance && (
          <div className="grid-2">
            <Field label={t("incident.starts_label")} htmlFor="edit-starts">
              <Input id="edit-starts" mono type="datetime-local" value={startsAt} onChange={(event) => setStartsAt(event.target.value)} required />
            </Field>
            <Field label={t("incident.ends_label")} htmlFor="edit-ends">
              <Input id="edit-ends" mono type="datetime-local" value={endsAt} onChange={(event) => setEndsAt(event.target.value)} required />
            </Field>
          </div>
        )}
        <Field label={t("incident.components_label")} help={t("incident.components_help")}>
          <div className="checks">
            {(components.data?.components ?? []).map((component) => (
              <label key={component.id}>
                <input type="checkbox" checked={selected.includes(component.id)} onChange={() => toggle(component.id)} />
                {component.name}
              </label>
            ))}
          </div>
        </Field>
        {error !== null && <Pill tone="danger">{t(error)}</Pill>}
        <div className="cluster">
          <Button type="submit" variant="primary" icon="check" disabled={busy || selected.length === 0}>
            {t("incident.save")}
          </Button>
          <Button variant="ghost" onClick={cancel}>
            {t("incident.cancel")}
          </Button>
        </div>
      </form>
    </Card>
  );
}
