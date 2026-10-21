import { useState } from "react";
import { Link } from "react-router";

import { useAudit, type AuditEvent, type AuditObject } from "../api/audit";
import { Button } from "../components/Button";
import { Card, EmptyCard } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Table } from "../components/Table";
import { useT } from "../i18n/context";
import { actorLabel, detailEntries, objectLabel, verbOf } from "../lib/audit";
import { formatClock } from "../lib/time";
import "./Audit.scss";

// Le journal : qui a fait quoi, quand, sur quel objet, du plus récent au plus
// ancien, par pages. Cliquer un objet ne montre que son histoire. Le détail
// d'un geste reste replié.
export function Audit() {
  const t = useT();
  const [followed, setFollowed] = useState<AuditObject | null>(null);
  // Les curseurs des pages déjà quittées : revenir, c'est dépiler.
  const [cursors, setCursors] = useState<number[]>([]);
  const before = cursors.at(-1) ?? null;
  const audit = useAudit(before, followed);
  const events = audit.data?.events ?? [];
  const nextBefore = audit.data?.next_before ?? null;

  const follow = (object: AuditObject | null) => {
    setFollowed(object);
    setCursors([]);
  };

  return (
    <>
      <PageHead title={t("audit.title")} subtitle={t("audit.subtitle")} />
      {followed !== null && (
        <div className="audit-filter">
          <span>{t("audit.following", t(`audit.object_${followed.kind}`), followed.name)}</span>
          <Button small icon="x" onClick={() => follow(null)}>
            {t("audit.clear_filter")}
          </Button>
        </div>
      )}
      {audit.error && <Failure error={audit.error} />}
      {audit.data && events.length === 0 && <EmptyCard icon="file" title={t("audit.empty")} text={t("audit.empty_text")} />}
      {audit.data && events.length > 0 && (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("audit.col_when")}</th>
                <th>{t("audit.col_actor")}</th>
                <th>{t("audit.col_action")}</th>
                <th>{t("audit.col_object")}</th>
                <th className="wide-only">{t("audit.col_details")}</th>
              </tr>
            </thead>
            <tbody>
              {events.map((event) => (
                <AuditRow key={event.id} event={event} onFollow={follow} />
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      {(cursors.length > 0 || nextBefore !== null) && (
        <div className="audit-pages">
          <Button small disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>
            {t("audit.newer")}
          </Button>
          <Button small disabled={nextBefore === null} onClick={() => nextBefore !== null && setCursors([...cursors, nextBefore])}>
            {t("audit.older")}
          </Button>
        </div>
      )}
    </>
  );
}

function AuditRow({ event, onFollow }: { event: AuditEvent; onFollow: (object: AuditObject) => void }) {
  const t = useT();
  const name = objectLabel(event);
  const details = detailEntries(event.details);
  return (
    <tr>
      <td className="nowrap mono">{formatClock(event.at)}</td>
      <td className={event.actor_kind === "unknown" ? "audit-unknown" : "secondary"}>{actorLabel(t, event)}</td>
      <td>{t(`audit.verb_${verbOf(event.action)}`)}</td>
      <td>
        <span className="muted">{t(`audit.object_${event.object_kind}`)}</span>{" "}
        {event.object_id !== "" ? (
          <button
            type="button"
            className="audit-object"
            title={t("audit.follow")}
            onClick={() => onFollow({ kind: event.object_kind, id: event.object_id, name: name })}
          >
            {name}
          </button>
        ) : (
          <span>{name}</span>
        )}
      </td>
      <td className="wide-only">
        {(details.length > 0 || event.apply_id !== null) && (
          <details className="audit-details">
            <summary>{t("audit.show_details")}</summary>
            <dl>
              {details.map(([key, value]) => (
                <div key={key}>
                  <dt className="mono">{key}</dt>
                  <dd className="mono">{value}</dd>
                </div>
              ))}
            </dl>
            {event.apply_id !== null && <Link to={`/services/annuaire/journal/${event.apply_id}`}>{t("audit.apply")}</Link>}
          </details>
        )}
      </td>
    </tr>
  );
}
