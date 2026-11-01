import { deleteMute, useMutes, type Mute } from "../../api/alerts";
import { useRefresh } from "../../api/refresh";
import { Button } from "../../components/Button";
import { ConfirmInline } from "../../components/ConfirmInline";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { SettingBlock, SettingFoot } from "../../components/SettingRow";
import { Table } from "../../components/Table";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT, type Translate } from "../../i18n/context";
import { muteStateOf, muteTargets, newMutePath, type MuteState } from "../../lib/alerts";
import type { Tone } from "../../lib/tone";
import { formatClock } from "../../lib/time";

const muteStateTones: Record<MuteState, Tone> = {
  active: "accent",
  planned: "warn",
  expired: "neutral",
};

// Les silences : ce qui tait les alertes, tout de suite ou à l'heure dite. Une
// ligne par silence, avec son état et son auteur ; la pose est dans le pied
// du bloc, sa fenêtre à son adresse.
export function MutesBlock() {
  const t = useT();
  const mutes = useMutes();
  return (
    <>
      <div className="setting-subhead">
        <span className="card-t">{t("silence.title")}</span>
        <span className="secondary">{t("silence.intro")}</span>
      </div>
      {mutes.data === null ? (
        <SettingBlock>
          <Failure error={mutes.error} />
        </SettingBlock>
      ) : mutes.data.mutes.length === 0 ? (
        <SettingBlock>
          <p className="secondary">{t("silence.empty_text")}</p>
        </SettingBlock>
      ) : (
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
            {mutes.data.mutes.map((mute) => (
              <MuteRow key={mute.id} mute={mute} />
            ))}
          </tbody>
        </Table>
      )}
      <SettingFoot>
        <Button icon="pause" to={newMutePath}>
          {t("silence.add")}
        </Button>
      </SettingFoot>
    </>
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
      onSettled: () => refresh("alerts"),
    });
  };
  return (
    <tr>
      <td>
        <span className="cell-stack">
          <span>{muteTargets(t, mute)}</span>
          <Pill tone={muteStateTones[state]}>{t(`silence.state_${state}`)}</Pill>
          {mute.announce && (
            <span>
              <Pill tone="accent">{t("silence.announced_pill")}</Pill> <span className="secondary">{mute.public_title}</span>
            </span>
          )}
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
            <ConfirmInline question={t("silence.delete_confirm")} onConfirm={lift} variant="ghost" small disabled={action.busy}>
              {t("silence.delete")}
            </ConfirmInline>
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
