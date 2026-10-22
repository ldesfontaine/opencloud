import { useState } from "react";
import { Link } from "react-router";

import { testChannel, useChannels, type Channel, type ChannelTest } from "../../api/alerts";
import { Button } from "../../components/Button";
import { Card, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { Subject, Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { channelTestResult } from "../../lib/alerts";
import "./Alerts.scss";

export function ChannelsTab() {
  const t = useT();
  const channels = useChannels();
  return (
    <Loaded resource={channels}>
      {(data) =>
        data.channels.length === 0 ? (
          <EmptyCard icon="bell" title={t("channel.empty_title")} text={t("channel.empty_text")} />
        ) : (
          <Card className="scroll-x">
            <Table>
              <thead>
                <tr>
                  <th>{t("channel.col_channel")}</th>
                  <th>{t("channel.col_format")}</th>
                  <th>{t("channel.col_severity")}</th>
                  <th className="th-end">{t("channel.col_state")}</th>
                  <th className="th-end"></th>
                </tr>
              </thead>
              <tbody>
                {data.channels.map((channel) => (
                  <ChannelRow key={channel.id} channel={channel} />
                ))}
              </tbody>
            </Table>
          </Card>
        )
      }
    </Loaded>
  );
}

// Une ligne de canal : son nom, son URL, et un essai qui répond sur place,
// le même que celui de sa fiche.
function ChannelRow({ channel }: { channel: Channel }) {
  const t = useT();
  const [test, setTest] = useState<ChannelTest | null>(null);
  const action = useAction();
  const startTest = () => {
    setTest(null);
    action.run(() => testChannel(channel.id), { onDone: setTest });
  };
  return (
    <tr>
      <td>
        <span className="cell-stack">
          <Link className="svc" to={`/alertes/canaux/${channel.id}`}>
            <Subject icon="bell">{channel.name}</Subject>
          </Link>
          {channel.receives_audit && <Pill tone="neutral">{t("channel.receives_audit_pill")}</Pill>}
          {/* Un e-mail n'a pas d'URL : la ligne montre ses destinataires. */}
          <span className="secondary mono channel-url">{channel.format === "email" ? channel.destination : channel.url}</span>
        </span>
      </td>
      <td>{t(`channel.format_${channel.format}`)}</td>
      <td className="secondary">{t(`channel.receives_${channel.min_severity}`)}</td>
      <td className="td-end">
        <Pill tone={channel.enabled ? "ok" : "neutral"}>{t(channel.enabled ? "channel.state_enabled" : "channel.state_disabled")}</Pill>
      </td>
      <td className="td-end">
        <span className="cell-action">
          {test !== null && <Pill tone={test.ok ? "ok" : "danger"}>{channelTestResult(t, test)}</Pill>}
          <Failure error={action.error} />
          <Button small icon="refresh" onClick={startTest} disabled={action.busy}>
            {t("channel.test")}
          </Button>
        </span>
      </td>
    </tr>
  );
}
