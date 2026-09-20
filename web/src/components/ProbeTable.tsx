import { Link } from "react-router";

import type { Probe, ProbeDay } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { uptimeOfDays, uptimePercent } from "../lib/probes";
import { ago } from "../lib/time";
import { formatDecimal } from "../lib/units";
import { Subject, Table } from "./Table";
import { ProbePill } from "./Pill";
import { UptimeBar } from "./UptimeBar";

// Les jours montrés dans la liste : la barre de la planche, sur un mois.
const listSpan = 30;

export function ProbeTable({ probes, days, withMachine = false }: { probes: Probe[]; days: Record<string, ProbeDay[]>; withMachine?: boolean }) {
  const { t, language } = useI18n();
  const now = useNow();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("probes.col_probe")}</th>
          <th>{t("probes.col_target")}</th>
          {withMachine && <th>{t("probes.col_machine")}</th>}
          <th>{t("probes.col_state")}</th>
          <th>{t("probes.col_uptime")}</th>
          <th>{t("probes.col_response")}</th>
          <th className="th-end">{t("probes.col_checked")}</th>
        </tr>
      </thead>
      <tbody>
        {probes.map((probe) => {
          const counts = uptimeOfDays(days[probe.id] ?? []);
          const percent = uptimePercent(counts.total, counts.success);
          return (
            <tr key={probe.id}>
              <td>
                <Link className="svc" to={`/domaines/${probe.id}`}>
                  <Subject icon="globe">{probe.name}</Subject>
                </Link>
              </td>
              <td className="mono">{probe.target}</td>
              {withMachine && (
                <td>
                  <Link to={`/machines/${probe.machine_id}`}>{probe.machine_name}</Link>
                </td>
              )}
              <td>
                <ProbePill status={probe.status} />
              </td>
              <td>
                <span className="ubar-cell">
                  <UptimeBar days={days[probe.id] ?? []} span={listSpan} now={now} />
                  <span className="mono secondary">{percent === null ? "—" : `${formatDecimal(language, percent, 1)} %`}</span>
                </span>
              </td>
              <td className="num">{probe.last_checked_at === null ? "—" : t("time.milliseconds", probe.last_duration_ms)}</td>
              <td className="num td-end secondary nowrap">{probe.last_checked_at === null ? t("probe.never") : ago(t, probe.last_checked_at, now)}</td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}
