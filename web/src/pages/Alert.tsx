import { Link, useParams } from "react-router";

import type { AlertResponse, ChannelsResponse } from "../api/types";
import { Card, CardBody, CardHeader, Empty, KeyValue } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { List } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useDiskThresholds, useT } from "../i18n/context";
import { deliveryReason, deliveryTones, describeAlert } from "../lib/alerts";
import { formatClock } from "../lib/time";
import { AlertRow } from "./Alerts";
import { NotFound } from "./NotFound";
import "./Alerts.scss";

// La fiche d'une alerte : ses trois lignes, ses dates, et ce qui en est
// parti vers chaque canal.
export function AlertPage() {
  const { id = "" } = useParams();
  const t = useT();
  const now = useNow();
  const disks = useDiskThresholds();
  const detail = useResource<AlertResponse>(`/api/alerts/${id}`);
  const channels = useResource<ChannelsResponse>("/api/alerts/channels");
  if (detail.error?.status === 404) {
    return <NotFound />;
  }
  if (detail.error) {
    return <Failure error={detail.error} />;
  }
  if (!detail.data) {
    return null;
  }
  const alert = detail.data.alert;
  const text = describeAlert(t, alert, now, disks);
  const channelName = (channelID: number) => channels.data?.channels.find((channel) => channel.id === channelID)?.name ?? `#${channelID}`;
  return (
    <>
      <PageHead title={text.fact} subtitle={text.cause} />
      <Card>
        <List>
          <AlertRow alert={alert} />
        </List>
      </Card>
      <div className="grid-2">
        <Card>
          <CardHeader title={t("alerts.col_state")} />
          <CardBody gap={8}>
            <KeyValue label={t("alert.status_open")} value={formatClock(alert.opened_at)} mono />
            {alert.acknowledged_at !== null && <KeyValue label={t("alert.status_acknowledged")} value={formatClock(alert.acknowledged_at)} mono />}
            {alert.resolved_at !== null && <KeyValue label={t("alert.status_resolved")} value={formatClock(alert.resolved_at)} mono />}
            <KeyValue label={t("alert.severity_attention") + " / " + t("alert.severity_danger")} value={t(`alert.severity_${alert.severity}`)} />
            {alert.silenced && <span className="secondary">{t("alert.silenced_help")}</span>}
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("alert.deliveries")} />
          {detail.data.deliveries.length === 0 ? (
            <Empty text={t("alert.no_delivery")} />
          ) : (
            <List>
              {detail.data.deliveries.map((delivery) => (
                <div className="row" key={delivery.id}>
                  <span className="txt">
                    <b>
                      <Link to={`/alertes/canaux/${delivery.channel_id}`}>{channelName(delivery.channel_id)}</Link> · {t(`alert.event_${delivery.event}`)}
                    </b>
                    {delivery.status === "failed" && <span>{deliveryReason(t, delivery)}</span>}
                  </span>
                  <Pill tone={deliveryTones[delivery.status]}>{t(`alert.delivery_${delivery.status}`)}</Pill>
                  <span className="when">{formatClock(delivery.updated_at)}</span>
                </div>
              ))}
            </List>
          )}
        </Card>
      </div>
    </>
  );
}
