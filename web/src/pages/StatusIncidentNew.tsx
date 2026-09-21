import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";

import { api, errorKey } from "../api/client";
import type { Impact, Incident, IncidentStatus, StatusComponentsResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Note } from "../components/Card";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { fromLocalInput, statusesFor } from "../lib/status";
import "./StatusAdmin.scss";

type Kind = "incident" | "maintenance";

// Ouvrir un incident ou planifier une maintenance : un titre, un impact,
// un premier message, les composants touchés, et pour une maintenance sa
// fenêtre. Le serveur valide et répond par la clé du refus.
export function StatusIncidentNew() {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const components = useResource<StatusComponentsResponse>("/api/status/components");
  const [kind, setKind] = useState<Kind>("incident");
  const [title, setTitle] = useState("");
  const [impact, setImpact] = useState<Impact>("down");
  const [status, setStatus] = useState<IncidentStatus>("investigating");
  const [message, setMessage] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [startsAt, setStartsAt] = useState("");
  const [endsAt, setEndsAt] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const effectiveImpact: Impact = kind === "maintenance" ? "maintenance" : impact;
  const effectiveStatus: IncidentStatus = kind === "maintenance" ? "scheduled" : status;
  const list = components.data?.components ?? [];

  const toggle = (id: string) => {
    setSelected((current) => (current.includes(id) ? current.filter((candidate) => candidate !== id) : [...current, id]));
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<Incident>("/api/status/incidents", {
        title,
        impact: effectiveImpact,
        status: effectiveStatus,
        message,
        component_ids: selected,
        starts_at: kind === "maintenance" ? fromLocalInput(startsAt) : null,
        ends_at: kind === "maintenance" ? fromLocalInput(endsAt) : null,
      })
      .then((created) => {
        refresh("status");
        void navigate(`/page-statut/incidents/${created.id}`);
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead title={t("incident.new_title")} subtitle={t("incident.new_subtitle")} />
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("incident.kind_label")} htmlFor="incident-kind">
            <Select id="incident-kind" value={kind} onChange={(event) => setKind(event.target.value as Kind)}>
              <option value="incident">{t("incident.kind_incident")}</option>
              <option value="maintenance">{t("incident.kind_maintenance")}</option>
            </Select>
          </Field>
          <Field label={t("incident.title_label")} htmlFor="incident-title" help={t("incident.title_help")}>
            <Input id="incident-title" type="text" value={title} onChange={(event) => setTitle(event.target.value)} maxLength={120} autoComplete="off" autoFocus required />
          </Field>
          {kind === "incident" && (
            <div className="grid-2">
              <Field label={t("incident.impact_label")} htmlFor="incident-impact" help={t("incident.impact_help")}>
                <Select id="incident-impact" value={impact} onChange={(event) => setImpact(event.target.value as Impact)}>
                  <option value="degraded">{t("incident.impact_degraded")}</option>
                  <option value="down">{t("incident.impact_down")}</option>
                </Select>
              </Field>
              <Field label={t("incident.status_label")} htmlFor="incident-status">
                <Select id="incident-status" value={status} onChange={(event) => setStatus(event.target.value as IncidentStatus)}>
                  {statusesFor("down")
                    .filter((candidate) => candidate !== "resolved")
                    .map((candidate) => (
                      <option key={candidate} value={candidate}>
                        {t(`incident.status_${candidate}`)}
                      </option>
                    ))}
                </Select>
              </Field>
            </div>
          )}
          {kind === "maintenance" && (
            <div className="grid-2">
              <Field label={t("incident.starts_label")} htmlFor="incident-starts" help={t("incident.window_help")}>
                <Input id="incident-starts" mono type="datetime-local" value={startsAt} onChange={(event) => setStartsAt(event.target.value)} required />
              </Field>
              <Field label={t("incident.ends_label")} htmlFor="incident-ends">
                <Input id="incident-ends" mono type="datetime-local" value={endsAt} onChange={(event) => setEndsAt(event.target.value)} required />
              </Field>
            </div>
          )}
          <Field label={t("incident.message_label")} htmlFor="incident-message" help={t("incident.message_help")}>
            <textarea id="incident-message" className="input textarea" value={message} onChange={(event) => setMessage(event.target.value)} maxLength={2000} rows={3} />
          </Field>
          <Field label={t("incident.components_label")} help={t("incident.components_help")}>
            {list.length === 0 ? (
              <Note tone="warn">{t("statuspage.components_empty_text")}</Note>
            ) : (
              <div className="checks">
                {list.map((component) => (
                  <label key={component.id}>
                    <input type="checkbox" checked={selected.includes(component.id)} onChange={() => toggle(component.id)} />
                    {component.name}
                  </label>
                ))}
              </div>
            )}
          </Field>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon="plus" disabled={busy || selected.length === 0}>
              {kind === "maintenance" ? t("incident.schedule") : t("incident.open")}
            </Button>
            <Button variant="ghost" to="/page-statut/incidents">
              {t("incident.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
