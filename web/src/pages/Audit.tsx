import { useEffect, useState } from "react";
import { Link } from "react-router";

import { filterableActorKinds, useAudit, useAuditForwarding, type AuditActorKind, type AuditEvent, type AuditObject } from "../api/audit";
import { Button } from "../components/Button";
import { Card, EmptyCard, Note } from "../components/Card";
import { Failure } from "../components/Failure";
import { Input } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Select } from "../components/Select";
import { Table } from "../components/Table";
import { useT } from "../i18n/context";
import { actorParts, actorTone, detailEntries, objectLabel, verbOf } from "../lib/audit";
import { formatClock } from "../lib/time";
import "./Audit.scss";

const searchDelayMs = 300;

// Le journal : qui a fait quoi, quand, sur quel objet, du plus récent au plus
// ancien, par pages. Cliquer un objet ne montre que son histoire. Le détail
// d'un geste reste replié.
export function Audit() {
  const t = useT();
  const [followed, setFollowed] = useState<AuditObject | null>(null);
  // Les curseurs des pages déjà quittées : revenir, c'est dépiler.
  const [cursors, setCursors] = useState<number[]>([]);
  const before = cursors.at(-1) ?? null;
  const [actorKind, setActorKind] = useState<AuditActorKind | "">("");
  const [search, setSearch] = useState("");
  // La recherche part au serveur une fois la frappe posée, pas à chaque lettre.
  const [searched, setSearched] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setSearched(search.trim()), searchDelayMs);
    return () => clearTimeout(timer);
  }, [search]);
  const audit = useAudit(before, followed, { kind: actorKind, search: searched });
  const forwarding = useAuditForwarding().data;
  const events = audit.data?.events ?? [];
  const nextBefore = audit.data?.next_before ?? null;

  const follow = (object: AuditObject | null) => {
    setFollowed(object);
    setCursors([]);
  };
  const chooseKind = (kind: string) => {
    setActorKind(filterableActorKinds.find((known) => known === kind) ?? "");
    setCursors([]);
  };
  const searching = actorKind !== "" || searched !== "";
  const kindOptions = [
    { value: "", label: t("audit.filter_all_actors") },
    ...filterableActorKinds.map((kind) => ({ value: kind, label: t(`audit.actor_${kind}`) })),
  ];

  return (
    <>
      <PageHead title={t("audit.title")} subtitle={t("audit.subtitle")} />
      {forwarding?.channels === 0 && <Note tone="warn">{t("audit.forwarding_none")}</Note>}
      {forwarding && forwarding.failing > 0 && <Note tone="danger">{t("audit.forwarding_failing", forwarding.failing)}</Note>}
      {followed !== null && (
        <div className="audit-filter">
          <span>{t("audit.following", t(`audit.object_${followed.kind}`), followed.name)}</span>
          <Button small icon="x" onClick={() => follow(null)}>
            {t("audit.clear_filter")}
          </Button>
        </div>
      )}
      <div className="audit-actor-filter">
        <Select value={actorKind} options={kindOptions} onChange={chooseKind} aria-label={t("audit.filter_actor_kind")} />
        <Input
          type="search"
          value={search}
          maxLength={200}
          placeholder={t("audit.filter_actor_search")}
          aria-label={t("audit.filter_actor_search")}
          onChange={(event) => {
            setSearch(event.target.value);
            setCursors([]);
          }}
        />
      </div>
      {audit.error && <Failure error={audit.error} />}
      {audit.data && events.length === 0 && (
        <EmptyCard icon="file" title={t(searching ? "audit.empty_filtered" : "audit.empty")} text={t(searching ? "audit.empty_filtered_text" : "audit.empty_text")} />
      )}
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

// L'acteur se lit d'un coup d'œil : la pastille dit le genre, puis le nom du
// jeton ou de la personne, puis « pour : … » en évidence pour un jeton
// (discret quand il ne dit pas pour qui, ce qu'un jeton d'écriture doit
// laisser voir).
function Actor({ event }: { event: AuditEvent }) {
  const t = useT();
  const parts = actorParts(event);
  return (
    <span className="audit-actor">
      <Pill tone={actorTone(parts.kind)} small dot={false}>
        {t(`audit.actor_${parts.kind}`)}
      </Pill>
      {parts.name !== "" && <span className="mono">{parts.name}</span>}
      {parts.forWhom !== null &&
        (parts.forWhom === "" ? (
          <span className="muted">{t("audit.on_behalf_of_unknown")}</span>
        ) : (
          <strong className="audit-for-whom">{t("audit.on_behalf_of", parts.forWhom)}</strong>
        ))}
    </span>
  );
}

function AuditRow({ event, onFollow }: { event: AuditEvent; onFollow: (object: AuditObject) => void }) {
  const t = useT();
  const name = objectLabel(event);
  const details = detailEntries(event.details);
  return (
    <tr>
      <td className="nowrap mono">{formatClock(event.at)}</td>
      <td>
        <Actor event={event} />
      </td>
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
