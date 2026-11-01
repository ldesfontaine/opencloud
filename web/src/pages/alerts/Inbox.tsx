import { useState, type ReactNode } from "react";

import type { Alert } from "../../api/alerts";
import { Button } from "../../components/Button";
import { Card, Empty, EmptyCard } from "../../components/Card";
import { Disclosure } from "../../components/Disclosure";
import { Pill } from "../../components/Pill";
import { List } from "../../components/Table";
import { useT } from "../../i18n/context";
import { filterAlerts, inboxOf, isFiltering, type AlertFilter } from "../../lib/alerts";
import { AlertFilters } from "./AlertFilters";
import { AlertRow } from "./AlertRow";
import { SelectableAlerts } from "./SelectableAlerts";
import "./Alerts.scss";

// La boîte de réception : les non vues en tête, à marquer comme vues ; les
// vues dessous ; les résolues repliées au bas, c'est l'historique. Les
// filtres s'appliquent aux trois. resolved est null tant que l'historique
// n'est pas arrivé : la boîte s'affiche sans l'attendre.
export function Inbox({
  open,
  resolved,
  filter,
  serviceNames,
  onFilter,
  onClearFilter,
}: {
  open: Alert[];
  resolved: Alert[] | null;
  filter: AlertFilter;
  serviceNames: ReadonlyMap<string, string>;
  onFilter: (field: keyof AlertFilter, value: string) => void;
  onClearFilter: () => void;
}) {
  const t = useT();
  // Le retour d'un marquage groupé vit ici : il reste lisible même quand la
  // liste des non vues s'est vidée.
  const [notice, setNotice] = useState<string | null>(null);
  const filtering = isFiltering(filter);
  const { unseen, seen } = inboxOf(filterAlerts(open, filter));
  const history = filterAlerts(resolved ?? [], filter);
  const nothingOpen = open.length === 0;

  return (
    <>
      {(!nothingOpen || (resolved ?? []).length > 0) && (
        <AlertFilters alerts={[...open, ...(resolved ?? [])]} filter={filter} serviceNames={serviceNames} onChange={onFilter} onClear={onClearFilter} />
      )}
      {nothingOpen ? (
        <EmptyCard icon="bell" title={t("alerts.empty_open_title")} text={t("alerts.empty_open_text")} />
      ) : (
        <>
          <InboxSection title={t("alerts.section_unseen")} count={unseen.length} tone="danger">
            {unseen.length > 0 ? (
              <SelectableAlerts alerts={unseen} notice={notice} setNotice={setNotice} />
            ) : (
              <Card>
                {notice !== null && (
                  <div className="alert-bulk-result" role="status">
                    <Pill tone="ok">{notice}</Pill>
                  </div>
                )}
                <Empty icon="check" text={t(filtering ? "alerts.empty_filtered_text" : "alerts.empty_unseen_text")} action={filtering ? <ClearFilter onClear={onClearFilter} /> : undefined} />
              </Card>
            )}
          </InboxSection>
          {seen.length > 0 && (
            <InboxSection title={t("alerts.section_seen")} count={seen.length} tone="accent">
              <Card>
                <List>
                  {seen.map((alert) => (
                    <AlertRow key={alert.id} alert={alert} />
                  ))}
                </List>
              </Card>
            </InboxSection>
          )}
        </>
      )}
      {resolved !== null && resolved.length > 0 && (
        <Disclosure title={t("alerts.section_resolved")} aside={t("alerts.resolved_count", history.length)} initiallyOpen={false}>
          {history.length > 0 ? (
            <List>
              {history.map((alert) => (
                <AlertRow key={alert.id} alert={alert} />
              ))}
            </List>
          ) : (
            <Empty text={t("alerts.empty_filtered_text")} action={<ClearFilter onClear={onClearFilter} />} />
          )}
        </Disclosure>
      )}
    </>
  );
}

function ClearFilter({ onClear }: { onClear: () => void }) {
  const t = useT();
  return (
    <Button small icon="x" onClick={onClear}>
      {t("alerts.filter_clear")}
    </Button>
  );
}

// Une section de la boîte : son titre et la pastille de son compteur.
function InboxSection({ title, count, tone, children }: { title: string; count: number; tone: "danger" | "accent"; children: ReactNode }) {
  return (
    <section className="inbox-section">
      <h2 className="side-label inbox-title">
        {title}
        <Pill tone={tone} dot={false} small>
          {count}
        </Pill>
      </h2>
      {children}
    </section>
  );
}
