import { Link, useParams } from "react-router";

import { useAlert, useChannels, type AlertResponse, type Channel } from "../api/alerts";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue } from "../components/Card";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { List } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useDiskThresholds, useT } from "../i18n/context";
import { deliveryFailure, deliveryTones, describeAlert } from "../lib/alerts";
import { formatClock } from "../lib/time";
import { AlertRow } from "./alerts/AlertRow";

// La fiche d'une alerte : ses trois lignes, ses dates, et ce qui en est
// parti vers chaque canal.
export function AlertPage() {
  const { id = "" } = useParams();
  const detail = useAlert(id);
  const channels = useChannels();
  return <Loaded resource={detail}>{(data) => <AlertView detail={data} channels={channels.data?.channels ?? []} />}</Loaded>;
}

function AlertView({ detail, channels }: { detail: AlertResponse; channels: Channel[] }) {
  const t = useT();
  const now = useNow();
  const disks = useDiskThresholds();
  const alert = detail.alert;
  const text = describeAlert(t, alert, now, disks);
  const channelName = (channelID: number) => channels.find((channel) => channel.id === channelID)?.name ?? `#${channelID}`;
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
            <KeyValue label={t("alert.severity_label")} value={t(`alert.severity_${alert.severity}`)} />
            {alert.muted && <span className="secondary">{t("alert.muted_help")}</span>}
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("alert.deliveries")} />
          {detail.deliveries.length === 0 ? (
            <Empty text={t("alert.no_delivery")} action={<Button small icon="plus" to="/alertes/canaux/nouveau">{t("channel.add")}</Button>} />
          ) : (
            <List>
              {detail.deliveries.map((delivery) => (
                <div className="row" key={delivery.id}>
                  <span className="txt">
                    <b>
                      <Link to={`/alertes/canaux/${delivery.channel_id}`}>{channelName(delivery.channel_id)}</Link> · {t(`alert.event_${delivery.event}`)}
                    </b>
                    {delivery.status === "failed" && <span>{deliveryFailure(t, delivery)}</span>}
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
