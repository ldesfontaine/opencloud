import { Link, NavLink, useParams } from "react-router";

import { api } from "../api/client";
import type { Alert, AlertsResponse, Channel, ChannelsResponse, Silence, SilencesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardHeader, Empty } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Dot, Pill } from "../components/Pill";
import { List, Subject, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useDiskThresholds, useT } from "../i18n/context";
import { alertsSubtitle, describeAlert, objectPath, severityTones, silenceTargets, stateOf, stateTones } from "../lib/alerts";
import { useRefresh } from "../lib/refresh";
import { ago, formatClock } from "../lib/time";
import { NotFound } from "./NotFound";
import "./Alerts.scss";

const tabs = [
  { slug: "", key: "alerts.tab_open" },
  { slug: "acquittees", key: "alerts.tab_acknowledged" },
  { slug: "resolues", key: "alerts.tab_resolved" },
  { slug: "canaux", key: "alerts.tab_channels" },
  { slug: "silences", key: "alerts.tab_silences" },
];

// La page Alertes : les ouvertes, les acquittées, les résolues, puis les
// canaux et les silences, sous cinq onglets. Tout en direct.
export function Alerts() {
  const { tab = "" } = useParams();
  const t = useT();
  const counts = useResource<AlertsResponse>("/api/alerts");
  if (!tabs.some((candidate) => candidate.slug === tab)) {
    return <NotFound />;
  }
  const open = counts.data?.counts.open ?? 0;
  const unacknowledged = counts.data?.counts.unacknowledged ?? 0;
  return (
    <>
      <PageHead
        title={t("alerts.title")}
        subtitle={counts.data ? alertsSubtitle(t, open, unacknowledged) : ""}
        actions={
          <>
            {tab === "canaux" && (
              <Button variant="primary" icon="plus" to="/alertes/canaux/nouveau">
                {t("channel.add")}
              </Button>
            )}
            {tab === "silences" && (
              <Button variant="primary" icon="plus" to="/alertes/silences/nouveau">
                {t("silence.add")}
              </Button>
            )}
          </>
        }
      />
      <nav className="tabs">
        {tabs.map((entry) => (
          <NavLink key={entry.slug} className={({ isActive }) => (isActive ? "tab active" : "tab")} to={entry.slug === "" ? "/alertes" : `/alertes/${entry.slug}`} end>
            {t(entry.key)}
          </NavLink>
        ))}
      </nav>
      {tab === "" && <AlertsTab status="open" acknowledged={false} />}
      {tab === "acquittees" && <AlertsTab status="open" acknowledged={true} />}
      {tab === "resolues" && <AlertsTab status="resolved" acknowledged={null} />}
      {tab === "canaux" && <ChannelsTab />}
      {tab === "silences" && <SilencesTab />}
    </>
  );
}

// Une liste d'alertes : les ouvertes non acquittées, les acquittées, ou
// les résolues.
function AlertsTab({ status, acknowledged }: { status: "open" | "resolved"; acknowledged: boolean | null }) {
  const t = useT();
  const alerts = useResource<AlertsResponse>(status === "resolved" ? "/api/alerts?status=resolved" : "/api/alerts");
  if (alerts.error) {
    return <Failure error={alerts.error} />;
  }
  if (!alerts.data) {
    return null;
  }
  const list = alerts.data.alerts.filter((alert) => acknowledged === null || (alert.acknowledged_at !== null) === acknowledged);
  if (list.length === 0) {
    const text = status === "resolved" ? "alerts.empty_resolved_text" : acknowledged ? "alerts.empty_acknowledged_text" : "alerts.empty_open_text";
    return (
      <Card>
        {status === "open" && !acknowledged ? <Empty icon="bell" title={t("alerts.empty_open_title")} text={t(text)} /> : <Empty icon="bell" text={t(text)} />}
      </Card>
    );
  }
  return (
    <Card>
      <List>
        {list.map((alert) => (
          <AlertRow key={alert.id} alert={alert} />
        ))}
      </List>
    </Card>
  );
}

// Une alerte : le point de sa gravité, le fait, la cause, le geste ; à
// droite sa machine, son état, son moment, et ce qu'on peut en faire.
export function AlertRow({ alert, compact = false }: { alert: Alert; compact?: boolean }) {
  const t = useT();
  const now = useNow();
  const disks = useDiskThresholds();
  const { refresh } = useRefresh();
  const text = describeAlert(t, alert, now, disks);
  const state = stateOf(alert);
  const path = objectPath(alert.object, alert.machine_id);
  const acknowledge = () => {
    api
      .post(`/api/alerts/${alert.id}/actions/acknowledge`)
      .then(() => refresh("alerts"))
      .catch(() => refresh("alerts"));
  };
  const silenceHref = `/alertes/silences/nouveau?kind=${alert.kind}&object_kind=${alert.object.kind}&object_id=${encodeURIComponent(alert.object.id)}&object_name=${encodeURIComponent(alert.object.name)}`;
  return (
    <div className="row alert-row">
      <Dot tone={severityTones[alert.severity]} />
      <span className="txt">
        <Link to={`/alertes/alerte/${alert.id}`}>
          <b>{text.fact}</b>
        </Link>
        <span>{text.cause}</span>
        {!compact && <span className="action">{text.action}</span>}
      </span>
      <span className="aside">
        <span className="secondary">
          {path !== null ? <Link to={path}>{alert.object.name}</Link> : alert.object.name}
          {alert.machine_name !== "" && alert.object.kind !== "machine" && (
            <>
              {" · "}
              <Link to={`/machines/${alert.machine_id}`}>{alert.machine_name}</Link>
            </>
          )}
        </span>
        <span className="cluster">
          {alert.silenced && (
            <Pill tone="neutral" dot={false}>
              {t("alert.silenced")}
            </Pill>
          )}
          <Pill tone={stateTones[state]}>{t(`alert.status_${state}`)}</Pill>
          <span className="secondary nowrap">{when(t, alert, now)}</span>
        </span>
        {!compact && state !== "resolved" && (
          <span className="actions">
            {state === "open" && (
              <Button small icon="check" onClick={acknowledge}>
                {t("alert.acknowledge")}
              </Button>
            )}
            <Button small variant="ghost" to={silenceHref}>
              {t("alerts.silence")}
            </Button>
          </span>
        )}
      </span>
    </div>
  );
}

function when(t: ReturnType<typeof useT>, alert: Alert, now: number): string {
  if (alert.resolved_at !== null) {
    return t("alert.resolved_ago", ago(t, alert.resolved_at, now));
  }
  if (alert.acknowledged_at !== null) {
    return t("alert.acknowledged_ago", ago(t, alert.acknowledged_at, now));
  }
  return t("alert.opened_ago", ago(t, alert.opened_at, now));
}

function ChannelsTab() {
  const t = useT();
  const channels = useResource<ChannelsResponse>("/api/alerts/channels");
  if (channels.error) {
    return <Failure error={channels.error} />;
  }
  if (!channels.data) {
    return null;
  }
  if (channels.data.channels.length === 0) {
    return (
      <Card>
        <Empty icon="bell" title={t("channel.empty_title")} text={t("channel.empty_text")} />
      </Card>
    );
  }
  return (
    <Card className="scroll-x">
      <Table>
        <thead>
          <tr>
            <th>{t("channel.col_channel")}</th>
            <th>{t("channel.col_format")}</th>
            <th>{t("channel.col_severity")}</th>
            <th className="th-end">{t("channel.col_state")}</th>
          </tr>
        </thead>
        <tbody>
          {channels.data.channels.map((channel) => (
            <ChannelRow key={channel.id} channel={channel} />
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function ChannelRow({ channel }: { channel: Channel }) {
  const t = useT();
  return (
    <tr>
      <td>
        <span className="cell-stack">
          <Link className="svc" to={`/alertes/canaux/${channel.id}`}>
            <Subject icon="bell">{channel.name}</Subject>
          </Link>
          <span className="secondary mono channel-url">{channel.url}</span>
        </span>
      </td>
      <td>{t(`channel.format_${channel.format}`)}</td>
      <td className="secondary">{t(`channel.receives_${channel.min_severity}`)}</td>
      <td className="td-end">
        <Pill tone={channel.enabled ? "ok" : "neutral"}>{t(channel.enabled ? "channel.state_enabled" : "channel.state_disabled")}</Pill>
      </td>
    </tr>
  );
}

function SilencesTab() {
  const t = useT();
  const silences = useResource<SilencesResponse>("/api/alerts/silences");
  if (silences.error) {
    return <Failure error={silences.error} />;
  }
  if (!silences.data) {
    return null;
  }
  if (silences.data.silences.length === 0) {
    return (
      <Card>
        <Empty icon="pause" title={t("silence.empty_title")} text={t("silence.empty_text")} />
      </Card>
    );
  }
  return (
    <Card className="scroll-x">
      <CardHeader title={t("alerts.tab_silences")} />
      <Table>
        <thead>
          <tr>
            <th>{t("silence.col_silence")}</th>
            <th>{t("silence.col_reason")}</th>
            <th>{t("silence.col_until")}</th>
            <th className="th-end"></th>
          </tr>
        </thead>
        <tbody>
          {silences.data.silences.map((silence) => (
            <SilenceRow key={silence.id} silence={silence} />
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function SilenceRow({ silence }: { silence: Silence }) {
  const t = useT();
  const { refresh } = useRefresh();
  const lift = () => {
    if (!window.confirm(t("silence.delete_confirm"))) {
      return;
    }
    api
      .delete(`/api/alerts/silences/${silence.id}`)
      .then(() => refresh("alerts"))
      .catch(() => refresh("alerts"));
  };
  return (
    <tr>
      <td>
        <span className="cell-stack">
          <span>{silenceTargets(t, silence)}</span>
          <Pill tone={silence.active ? "accent" : "neutral"}>{t(silence.active ? "silence.state_active" : "silence.state_expired")}</Pill>
        </span>
      </td>
      <td className="secondary">{silence.reason}</td>
      <td className="mono secondary nowrap">{formatClock(silence.ends_at)}</td>
      <td className="td-end">
        {silence.active && (
          <Button small variant="ghost" onClick={lift}>
            {t("silence.delete")}
          </Button>
        )}
      </td>
    </tr>
  );
}
