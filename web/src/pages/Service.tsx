import { Link, useParams } from "react-router";

import { ApiError } from "../api/client";
import type { ServiceResponse, Transition } from "../api/types";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { LogViewer } from "../components/LogViewer";
import { modeText } from "../components/NetworkInspector";
import { PageHead } from "../components/PageHead";
import { Dot, ServicePill } from "../components/Pill";
import { List, RowText, RowWhen } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useI18n } from "../i18n/context";
import { findingText, formatBindings, servicePill, transitionTitle } from "../lib/services";
import { ago, formatClock, formatDuration, since } from "../lib/time";
import { formatBytes, formatDecimal } from "../lib/units";
import { NotFound } from "./NotFound";

// La fiche d'un service : la pastille et les faits, les transitions, les
// journaux en direct.
export function ServicePage() {
  const { id = "" } = useParams();
  const response = useResource<ServiceResponse>(`/api/services/${id}`);
  if (response.error instanceof ApiError && response.error.status === 404) {
    return <NotFound />;
  }
  if (!response.data) {
    return response.error ? <Failure error={response.error} /> : null;
  }
  return <ServiceView response={response.data} />;
}

function ServiceView({ response }: { response: ServiceResponse }) {
  const { t, language } = useI18n();
  const now = useNow();
  const service = response.service;
  return (
    <>
      <PageHead title={service.name} subtitle={service.group !== "" && service.group !== service.name ? service.group : ""} />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <ServicePill service={service} />
            <span className="meta">
              <Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>
              <span className="sep" />
              <span className="mono">{service.image}</span>
              {service.state === "running" && service.started_at !== null && (
                <>
                  <span className="sep" />
                  <span>{t("service.uptime", formatDuration(t, since(service.started_at, now)))}</span>
                </>
              )}
              {service.current && (
                <>
                  <span className="sep" />
                  <span className="mono">{formatDecimal(language, service.current.cpu_percent, 0)} %</span>
                  <span className="sep" />
                  <span className="mono">
                    {formatBytes(t, language, service.current.mem_used)} / {formatBytes(t, language, service.current.mem_limit)}
                  </span>
                </>
              )}
            </span>
          </span>
        </CardBody>
      </Card>
      <div className="grid-3">
        <Card>
          <CardHeader title={t("tab.summary")} />
          <CardBody gap={10}>
            <KeyValue label={t("service.field_machine")} value={<Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>} />
            {service.group !== "" && <KeyValue label={t("service.field_group")} value={service.group} mono />}
            <KeyValue label={t("service.field_image")} value={service.image} mono />
            {service.image_id !== "" && <KeyValue label={t("service.field_image_id")} value={service.image_id.replace("sha256:", "").slice(0, 12)} mono />}
            <KeyValue label={t("service.field_container")} value={service.container_id.slice(0, 12)} mono />
            <KeyValue label={t("service.field_restarts")} value={service.restart_count} mono />
            <KeyValue label={t("service.field_created")} value={formatClock(service.created_at)} mono />
            {service.started_at !== null && <KeyValue label={t("service.field_started")} value={formatClock(service.started_at)} mono />}
            {service.finished_at !== null && service.state !== "running" && (
              <KeyValue
                label={t("service.field_finished")}
                value={`${formatClock(service.finished_at)} · ${t("service.exit_code", service.exit_code)}`}
                mono
              />
            )}
            <KeyValue label={t("service.field_seen")} value={ago(t, service.last_seen_at, now)} />
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("tab.network")} aside={<Link to={`/machines/${service.machine_id}/reseau`}>{t("network.open_graph")}</Link>} />
          <CardBody gap={10}>
            <KeyValue label={t("network.field_mode")} value={modeText(t, service)} mono />
            <KeyValue
              label={t("service.field_ports")}
              value={service.ports.length > 0 ? formatBindings(service.ports).map((line) => <div key={line}>{line}</div>) : t("service.internal")}
              mono
            />
            <KeyValue
              label={t("network.field_networks")}
              value={service.networks.length > 0 ? service.networks.map((attachment) => <div key={attachment.network_id}>{attachment.ip !== "" ? `${attachment.name} · ${attachment.ip}` : attachment.name}</div>) : t("network.none")}
              mono
            />
            <KeyValue
              label={t("network.field_depends")}
              value={service.depends_on.length > 0 ? service.depends_on.map((dependency) => dependency.name).join(", ") : t("network.none")}
              mono
            />
            {service.exposure.map((finding) =>
              finding.level === "warn" ? (
                <Note tone="warn" key={finding.kind + String(finding.port)}>
                  {findingText(t, finding)}
                </Note>
              ) : (
                <span className="secondary" key={finding.kind + String(finding.port)}>
                  {findingText(t, finding)}
                </span>
              ),
            )}
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("service.transitions_title")} />
          {response.transitions.length > 0 ? (
            <List>
              {response.transitions.map((transition) => (
                <TransitionRow key={transition.id} transition={transition} now={now} />
              ))}
            </List>
          ) : (
            <Empty text={t("service.transitions_empty")} />
          )}
        </Card>
      </div>
      <Card>
        <CardHeader title={t("service.logs_title")} />
        <LogViewer serviceID={service.id} />
      </Card>
    </>
  );
}

function TransitionRow({ transition, now }: { transition: Transition; now: number }) {
  const t = useI18n().t;
  const tone = transition.new_state === "" ? "neutral" : servicePill({ state: transition.new_state, exit_code: transition.exit_code ?? 0, health: transition.new_health }).tone;
  return (
    <div className="row">
      <Dot tone={tone} />
      <RowText
        title={
          <>
            {transitionTitle(t, transition)}
            {transition.exit_code !== null && (
              <>
                {" · "}
                <span className="mono">{t("service.exit_code", transition.exit_code)}</span>
              </>
            )}
            {transition.replayed && (
              <>
                {" · "}
                <span className="muted">{t("service.transition_replayed")}</span>
              </>
            )}
          </>
        }
      >
        {formatClock(transition.at)}
        {transition.snippet !== "" && (
          <>
            <br />
            <span className="mono">{transition.snippet.split("\n").slice(-3).join(" ⏎ ")}</span>
          </>
        )}
      </RowText>
      <RowWhen>{ago(t, transition.at, now)}</RowWhen>
    </div>
  );
}
