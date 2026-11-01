import { useState } from "react";

import { useMachineSystemdUnits, type SystemdUnit } from "../api/systemd";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { activeTone, declareUnitPath, isShown, systemdViews, type SystemdView } from "../lib/systemd";
import { formatWhen } from "../lib/time";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, EmptyLine } from "./Card";
import { Failure } from "./Failure";
import { Field, Input } from "./Field";
import { Pill } from "./Pill";
import { EnumSelect } from "./Select";
import { Table } from "./Table";
import { Link } from "react-router";
import "./MachineSystemdUnits.scss";

// L'onglet « Unités systemd » d'une machine : ce que son agent a relevé, avec
// l'état de chaque unité et le service déclaré qui la porte. Une unité que
// personne ne déclare s'ouvre dans l'assistant d'un clic. Lecture seule : le
// seul geste est de déclarer. Le direct relit sur le sujet des machines.
export function MachineSystemdUnits({ machineID }: { machineID: string }) {
  const t = useT();
  const now = useNow();
  const units = useMachineSystemdUnits(machineID);
  const [query, setQuery] = useState("");
  const [view, setView] = useState<SystemdView>("all");
  const report = units.data;
  if (units.error && !report) {
    return <Failure error={units.error} />;
  }
  if (!report) {
    return null;
  }
  const shown = report.units.filter((unit) => isShown(unit, view, query));
  return (
    <div className="systemd-units">
      {units.error && <Failure error={units.error} />}
      <Card>
        <CardHeader title={t("systemd.machine_title")} />
        <CardBody gap={8}>
          <span className="secondary">{t("systemd.intro")}</span>
          <span className="secondary">{report.reported_at === null ? t("systemd.never_reported") : t("systemd.reported", formatWhen(report.reported_at, now))}</span>
          {report.reported_at !== null && !report.available && (
            <span className="secondary">
              {t("systemd.unavailable")} {report.reason !== "" && t(`systemd.reason_${report.reason}`)}
            </span>
          )}
          {report.truncated && <span className="secondary">{t("systemd.truncated")}</span>}
        </CardBody>
      </Card>
      {report.available && (
        <Card className="scroll-x">
          <CardBody gap={12}>
            <div className="systemd-filters">
              <Field label={t("systemd.filter_label")} htmlFor="systemd-filter">
                <Input id="systemd-filter" type="search" value={query} placeholder={t("systemd.filter_placeholder")} onChange={(event) => setQuery(event.target.value)} />
              </Field>
              <Field label={t("systemd.view_label")} htmlFor="systemd-view">
                <EnumSelect id="systemd-view" value={view} values={systemdViews} labelKey="systemd.view_" onChange={setView} />
              </Field>
            </div>
            <span className="secondary">{t("systemd.count", shown.length, report.units.length)}</span>
          </CardBody>
          {shown.length > 0 ? (
            <Table>
              <thead>
                <tr>
                  <th>{t("systemd.col_unit")}</th>
                  <th>{t("systemd.col_state")}</th>
                  <th>{t("systemd.col_service")}</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((unit) => (
                  <UnitRow key={unit.name} unit={unit} machineID={machineID} />
                ))}
              </tbody>
            </Table>
          ) : (
            <EmptyLine text={t("systemd.none_match")} />
          )}
        </Card>
      )}
    </div>
  );
}

function UnitRow({ unit, machineID }: { unit: SystemdUnit; machineID: string }) {
  const t = useT();
  return (
    <tr>
      <td className="mono">{unit.name}</td>
      <td>
        <span className="cluster">
          <Pill tone={activeTone(unit.active)}>{t(`systemd.active_${unit.active}`)}</Pill>
          <span className="secondary">{t(`systemd.sub_${unit.sub}`)}</span>
          {unit.load !== "loaded" && <Pill tone="neutral" dot={false}>{t(`systemd.load_${unit.load}`)}</Pill>}
        </span>
      </td>
      <td>
        {unit.declared !== null ? (
          <Link to={`/services/annuaire/${unit.declared.service_id}`}>{unit.declared.service_name}</Link>
        ) : (
          <span className="cluster">
            <span className="muted">{t("systemd.not_declared")}</span>
            <Button small variant="ghost" icon="plus" to={declareUnitPath(machineID, unit.name)} title={t("systemd.declare_title", unit.name)}>
              {t("systemd.declare")}
            </Button>
          </span>
        )}
      </td>
    </tr>
  );
}
