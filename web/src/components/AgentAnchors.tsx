import { useBackups } from "../api/backups";
import type { Agent, AnchorKind } from "../api/machines";
import { useT } from "../i18n/context";
import { anchorKindsFor, anchorLabelKey, blockingAnchors } from "../lib/anchors";
import { heldRights } from "../lib/rights";
import type { Tone } from "../lib/tone";
import "./AgentAnchors.scss";
import { Note } from "./Card";
import { TokenBox } from "./Copy";
import { Pill } from "./Pill";

const stateTones: Record<AnchorKind["state"], Tone> = { set: "ok", none: "warn", unreadable: "danger" };

// Les ancres de confiance locales d'un agent, telles qu'il les rapporte : ce que
// l'administrateur de la machine a posé lui-même, et que nul ordre ne change.
// Trois états par type : posées, aucune posée (l'ordre qu'elles bornent est
// refusé), illisibles (refusé aussi, le fichier est à corriger). Un agent
// d'avant les ancres ne les applique pas : la fiche le dit au lieu de faire
// croire qu'il n'en a aucune. La commande montrée se colle telle quelle, en root.
export function AgentAnchors({ agent }: { agent: Agent }) {
  const t = useT();
  const recipient = useBackups().data?.keyring?.recipient;
  if (agent.revoked_at !== null) {
    return null;
  }
  const { anchors } = agent;
  if (!anchors.reported) {
    return <Note tone="neutral">{t("anchor.not_reported")}</Note>;
  }
  const rights = heldRights(agent).map((right) => right.name);
  const relevant: string[] = anchorKindsFor(rights).map((kind) => kind.name);
  const blocking = blockingAnchors(anchors, rights);
  const unreadable = blocking.filter((kind) => kind.state === "unreadable");
  const none = blocking.filter((kind) => kind.state === "none");
  // La clé du trousseau est connue : sa commande se colle telle quelle. Les
  // valeurs des autres types viennent du refus d'un ordre, qui les nomme.
  const noneCommands = [...(none.some((kind) => kind.name === "backup.recipient") && recipient !== undefined ? [t("anchor.cmd_add", "backup.recipient", recipient)] : []), t("anchor.cmd_list")];
  return (
    <section className="agent-anchors" aria-label={t("anchor.title")}>
      <h4>{t("anchor.title")}</h4>
      <p className="secondary">{t("anchor.subtitle")}</p>
      <ul className="anchor-list">
        {anchors.kinds.map((kind) => (
          <li key={kind.name} data-anchor={kind.name}>
            <span>{t(anchorLabelKey(kind.name))}</span>
            <Pill tone={stateTones[kind.state]}>{t(`anchor.state_${kind.state}`)}</Pill>
            <span className="mono">{kind.state === "set" ? kind.values.join(" ") : ""}</span>
            {!relevant.includes(kind.name) && <span className="secondary">{t("anchor.needs_right", t(`machine.capability_${kind.right.split(".").join("_")}`))}</span>}
          </li>
        ))}
      </ul>
      {none.length > 0 && (
        <Note tone="warn">
          <strong>{t("anchor.none_title")}</strong>
          <p>{t("anchor.none_text")}</p>
          {noneCommands.map((command) => (
            <TokenBox key={command} value={command} />
          ))}
        </Note>
      )}
      {unreadable.length > 0 && (
        <Note tone="danger">
          <strong>{t("anchor.unreadable_title")}</strong>
          <p>{t("anchor.unreadable_text")}</p>
          <TokenBox value={t("anchor.cmd_list")} />
        </Note>
      )}
    </section>
  );
}
