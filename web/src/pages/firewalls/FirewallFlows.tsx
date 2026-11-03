import type { FirewallFlowsResponse, FlowCell, FlowRule, FlowZone } from "../../api/firewalls";
import { Card, CardHeader, EmptyCard } from "../../components/Card";
import { Fold } from "../../components/Disclosure";
import { useT } from "../../i18n/context";
import { counted } from "../../lib/counted";
import { cellOf, cellSummary, flowLine, zoneName, zoneOf } from "../../lib/flows";

// « Qui parle à qui » : une ligne par zone de départ, une colonne par zone
// d'arrivée. Chaque case dit, une règle par ligne, ce qui passe ; une case
// vide veut dire refusé. Lecture seule, bâtie sur le modèle des échanges.
export function FirewallFlows({ flows }: { flows: FirewallFlowsResponse }) {
  const t = useT();
  if (flows.rows.length === 0 || flows.cells.length === 0 && flows.unplaced.length === 0) {
    return <EmptyCard icon="shield" text={t("firewall.flow_no_rules")} />;
  }
  return (
    <Card>
      <CardHeader title={t("firewall.flow_title")} aside={t("firewall.flow_hint")} />
      <ul className="firewall-legend">
        <li>
          <i className="firewall-legend-allow" />
          {t("firewall.flow_allow")}
        </li>
        <li>
          <i className="firewall-legend-deny" />
          {t("firewall.flow_deny")}
        </li>
        <li>{t("firewall.flow_empty_cell")}</li>
        <li>{t("firewall.flow_same_zone")}</li>
      </ul>
      <div className="scroll-x firewall-grid-scroll">
        <table className="firewall-grid">
          <thead>
            <tr>
              <th scope="col" className="firewall-grid-corner">
                {t("firewall.flow_corner")}
              </th>
              {flows.columns.map((id) => (
                <ZoneHead key={id} zone={zoneOf(flows, id)} fallback={id} scope="col" />
              ))}
            </tr>
          </thead>
          <tbody>
            {flows.rows.map((row) => (
              <tr key={row}>
                <ZoneHead zone={zoneOf(flows, row)} fallback={row} scope="row" />
                {flows.columns.map((column) => (
                  <GridCell key={column} flows={flows} from={row} to={column} />
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <FlowNotes flows={flows} />
    </Card>
  );
}

function ZoneHead({ zone, fallback, scope }: { zone: FlowZone | undefined; fallback: string; scope: "row" | "col" }) {
  const t = useT();
  return (
    <th scope={scope} className={scope === "row" ? "firewall-grid-row" : undefined}>
      <b>{zone === undefined ? fallback : zoneName(t, zone)}</b>
      {zone !== undefined && zone.network !== "" && <small className="mono">{zone.network}</small>}
    </th>
  );
}

function GridCell({ flows, from, to }: { flows: FirewallFlowsResponse; from: string; to: string }) {
  const t = useT();
  const cell = cellOf(flows, from, to);
  const names = { from: zoneName(t, zoneOf(flows, from) ?? { id: from, label: "", kind: "network" }), to: zoneName(t, zoneOf(flows, to) ?? { id: to, label: "", kind: "network" }) };
  if (from === to) {
    return <td className="firewall-cell firewall-cell-same" aria-label={t("firewall.flow_same_label")} />;
  }
  if (cell === undefined) {
    return (
      <td className="firewall-cell firewall-cell-empty" aria-label={t("firewall.flow_cell_label", names.from, names.to, t("firewall.flow_refused"))}>
        <span aria-hidden="true">—</span>
      </td>
    );
  }
  return (
    <td className="firewall-cell" aria-label={t("firewall.flow_cell_label", names.from, names.to, cellSummary(t, cell))}>
      <CellLines cell={cell} flows={flows} />
    </td>
  );
}

function CellLines({ cell, flows }: { cell: FlowCell; flows: FirewallFlowsResponse }) {
  return (
    <ul className="firewall-lines">
      {cell.allowed.map((rule, index) => (
        <FlowLineItem key={`a${index}`} rule={rule} flows={flows} />
      ))}
      {cell.denied.map((rule, index) => (
        <FlowLineItem key={`d${index}`} rule={rule} flows={flows} />
      ))}
    </ul>
  );
}

function FlowLineItem({ rule, flows }: { rule: FlowRule; flows: FirewallFlowsResponse }) {
  const t = useT();
  const line = flowLine(t, rule, flows);
  return (
    <li className={rule.effect === "deny" ? "firewall-line firewall-line-deny" : "firewall-line"}>
      <span>
        {line.from} → {line.to} : <b>{line.what}</b>
      </span>
      {(line.why !== "" || rule.effect === "deny") && (
        <small>{rule.effect === "deny" ? (line.why === "" ? t("firewall.flow_denied") : `${t("firewall.flow_denied")} · ${line.why}`) : line.why}</small>
      )}
    </li>
  );
}

function FlowNotes({ flows }: { flows: FirewallFlowsResponse }) {
  const t = useT();
  return (
    <div className="firewall-notes">
      <p className="secondary">
        {t("firewall.flow_note")} {flows.disabled > 0 && counted(t, "firewall.flow_disabled", flows.disabled)}
      </p>
      {flows.unplaced.length > 0 && (
        <Fold label={t("firewall.flow_unplaced", flows.unplaced.length)} initiallyOpen={false}>
          <p className="secondary">{t("firewall.flow_unplaced_text")}</p>
          <ul className="firewall-lines">
            {flows.unplaced.map((rule, index) => (
              <FlowLineItem key={index} rule={rule} flows={flows} />
            ))}
          </ul>
        </Fold>
      )}
    </div>
  );
}
