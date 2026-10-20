import { deleteSilence, useSilences, type Silence } from "../../api/alerts";
import { useRefresh } from "../../api/refresh";
import { Button } from "../../components/Button";
import { Card, CardHeader, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { silenceTargets } from "../../lib/alerts";
import { formatClock } from "../../lib/time";
import "./Alerts.scss";

export function SilencesTab() {
  const t = useT();
  const silences = useSilences();
  return (
    <Loaded resource={silences}>
      {(data) =>
        data.silences.length === 0 ? (
          <EmptyCard icon="pause" title={t("silence.empty_title")} text={t("silence.empty_text")} />
        ) : (
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
                {data.silences.map((silence) => (
                  <SilenceRow key={silence.id} silence={silence} />
                ))}
              </tbody>
            </Table>
          </Card>
        )
      }
    </Loaded>
  );
}

function SilenceRow({ silence }: { silence: Silence }) {
  const t = useT();
  const { refresh } = useRefresh();
  const action = useAction();
  // On relit même après un refus : le silence a pu être levé ou expirer
  // entre-temps, et la ligne doit le dire.
  const lift = () => {
    action.run(() => deleteSilence(silence.id), {
      confirm: t("silence.delete_confirm"),
      onSettled: () => refresh("alerts"),
    });
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
        <span className="cell-action">
          <Failure error={action.error} />
          {silence.active && (
            <Button small variant="ghost" onClick={lift} disabled={action.busy}>
              {t("silence.delete")}
            </Button>
          )}
        </span>
      </td>
    </tr>
  );
}
