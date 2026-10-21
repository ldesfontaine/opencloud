import { deleteMute, useMutes, type Mute } from "../../api/alerts";
import { useRefresh } from "../../api/refresh";
import { Button } from "../../components/Button";
import { Card, CardHeader, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Loaded } from "../../components/Loaded";
import { Pill } from "../../components/Pill";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT, type Translate } from "../../i18n/context";
import { muteStateOf, muteTargets, type MuteState } from "../../lib/alerts";
import type { Tone } from "../../lib/tone";
import { formatClock } from "../../lib/time";
import "./Alerts.scss";

const muteStateTones: Record<MuteState, Tone> = {
  active: "accent",
  planned: "warn",
  expired: "neutral",
};

// Les silences, sous ce nom à l'écran : ce qui tait les alertes, tout de
// suite ou à l'heure dite.
export function MutesTab() {
  const t = useT();
  const mutes = useMutes();
  return (
    <Loaded resource={mutes}>
      {(data) =>
        data.mutes.length === 0 ? (
          <EmptyCard icon="pause" title={t("silence.empty_title")} text={t("silence.empty_text")} />
        ) : (
          <Card className="scroll-x">
            <CardHeader title={t("alerts.tab_silences")} />
            <Table>
              <thead>
                <tr>
                  <th>{t("silence.col_silence")}</th>
                  <th>{t("silence.col_reason")}</th>
                  <th>{t("silence.col_author")}</th>
                  <th>{t("silence.col_until")}</th>
                  <th className="th-end"></th>
                </tr>
              </thead>
              <tbody>
                {data.mutes.map((mute) => (
                  <MuteRow key={mute.id} mute={mute} />
                ))}
              </tbody>
            </Table>
          </Card>
        )
      }
    </Loaded>
  );
}

function MuteRow({ mute }: { mute: Mute }) {
  const t = useT();
  const now = useNow();
  const { refresh } = useRefresh();
  const action = useAction();
  const state = muteStateOf(mute, now);
  // On relit même après un refus : le silence a pu être levé ou expirer
  // entre-temps, et la ligne doit le dire.
  const lift = () => {
    action.run(() => deleteMute(mute.id), {
      confirm: t("silence.delete_confirm"),
      onSettled: () => refresh("alerts"),
    });
  };
  return (
    <tr>
      <td>
        <span className="cell-stack">
          <span>{muteTargets(t, mute)}</span>
          <Pill tone={muteStateTones[state]}>{t(`silence.state_${state}`)}</Pill>
        </span>
      </td>
      <td className="secondary">{mute.reason}</td>
      <td className="secondary">{authorOf(t, mute)}</td>
      <td className="mono secondary nowrap">
        {state === "planned" ? `${formatClock(mute.starts_at)} → ` : ""}
        {formatClock(mute.ends_at)}
      </td>
      <td className="td-end">
        <span className="cell-action">
          <Failure error={action.error} />
          {state !== "expired" && (
            <Button small variant="ghost" onClick={lift} disabled={action.busy}>
              {t("silence.delete")}
            </Button>
          )}
        </span>
      </td>
    </tr>
  );
}

// Qui l'a posé, avec les mots du journal ; rien pour un silence venu d'avant.
function authorOf(t: Translate, mute: Mute): string {
  if (mute.created_by_kind === "") {
    return "—";
  }
  const kind = t(`audit.actor_${mute.created_by_kind}`);
  return mute.created_by_name !== "" ? `${kind} · ${mute.created_by_name}` : kind;
}
