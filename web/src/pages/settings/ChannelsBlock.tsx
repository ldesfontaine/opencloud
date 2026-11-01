import { useState } from "react";
import { Link } from "react-router";

import { deleteChannel, testChannel, useChannels, type Channel, type ChannelTest } from "../../api/alerts";
import { Button } from "../../components/Button";
import { ConfirmModal } from "../../components/ConfirmModal";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingFoot } from "../../components/SettingRow";
import { Subject, Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { channelPathOf, channelTestResult, newChannelPath } from "../../lib/alerts";
import { settingsBlockAnchors } from "../../lib/settingsSections";
import "./ChannelsBlock.scss";

// Les canaux : où partent les alertes. Une ligne par canal, un essai qui
// répond sur place, la modification et la suppression ; l'ajout est dans le
// pied du bloc. Les trois ouvrent leurs fenêtres à leur adresse.
export function ChannelsBlock() {
  const t = useT();
  const channels = useChannels();
  return (
    <>
      <div className="setting-subhead" id={settingsBlockAnchors.channels}>
        <span className="card-t">{t("channel.title")}</span>
        <span className="secondary">{t("channel.intro")}</span>
      </div>
      {channels.data === null ? (
        <SettingBlock>
          <Failure error={channels.error} />
        </SettingBlock>
      ) : channels.data.channels.length === 0 ? (
        <SettingBlock>
          <p className="secondary">{t("channel.empty_text")}</p>
        </SettingBlock>
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("channel.col_channel")}</th>
              <th className="th-end">{t("channel.col_state")}</th>
              <th className="th-end"></th>
            </tr>
          </thead>
          <tbody>
            {channels.data.channels.map((channel) => (
              <ChannelRow key={channel.id} channel={channel} />
            ))}
          </tbody>
        </Table>
      )}
      <SettingFoot>
        <Button icon="plus" to={newChannelPath}>
          {t("channel.add")}
        </Button>
      </SettingFoot>
    </>
  );
}

function ChannelRow({ channel }: { channel: Channel }) {
  const t = useT();
  const [test, setTest] = useState<ChannelTest | null>(null);
  const action = useAction();
  const startTest = () => {
    setTest(null);
    action.run(() => testChannel(channel.id), { onDone: setTest });
  };
  const remove = () => action.run(() => deleteChannel(channel.id), { topic: "alerts" });
  return (
    <tr>
      <td>
        <span className="cell-stack">
          <Link className="svc" to={channelPathOf(channel.id)}>
            <Subject icon="bell">{channel.name}</Subject>
          </Link>
          {channel.receives_audit && <Pill tone="neutral">{t("channel.receives_audit_pill")}</Pill>}
          <span className="secondary">
            {t(`channel.format_${channel.format}`)} · {t("channel.receives_line", t(`channel.receives_${channel.min_severity}`))}
          </span>
          {/* Un e-mail n'a pas d'URL : la ligne montre ses destinataires. */}
          <span className="secondary mono channel-url">{channel.format === "email" ? channel.destination : channel.url}</span>
        </span>
      </td>
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
          <ConfirmModal small variant="ghost" title={t("channel.delete")} question={t("channel.delete_confirm", channel.name)} confirmLabel={t("channel.delete")} onConfirm={remove} disabled={action.busy}>
            {t("channel.delete")}
          </ConfirmModal>
        </span>
      </td>
    </tr>
  );
}
