import type { Alert } from "../../api/alerts";
import { useMachines } from "../../api/machines";
import { Button } from "../../components/Button";
import { Select } from "../../components/Select";
import { useT } from "../../i18n/context";
import { alertSeverities, filterChoicesOf, isFiltering, type AlertFilter } from "../../lib/alerts";
import { kindsByID, machineOption } from "../../lib/machines";
import "./Alerts.scss";

// Les filtres de la boîte : gravité, type, machine, service. Ils ne proposent
// que ce que les alertes lues portent. serviceNames : le nom d'un service
// déclaré, que l'alerte ne porte pas (elle ne garde que son identifiant).
export function AlertFilters({
  alerts,
  filter,
  serviceNames,
  onChange,
  onClear,
}: {
  alerts: Alert[];
  filter: AlertFilter;
  serviceNames: ReadonlyMap<string, string>;
  onChange: (field: keyof AlertFilter, value: string) => void;
  onClear: () => void;
}) {
  const t = useT();
  const choices = filterChoicesOf(alerts, filter);
  const kinds = kindsByID(useMachines().data?.machines ?? []);
  return (
    <div className="alert-filters">
      <Select
        value={filter.severity}
        options={[{ value: "", label: t("alerts.filter_all_severities") }, ...alertSeverities.map((severity) => ({ value: severity, label: t(`alert.severity_${severity}`) }))]}
        onChange={(value) => onChange("severity", value)}
        aria-label={t("alerts.filter_severity")}
      />
      <Select
        value={filter.kind}
        options={[{ value: "", label: t("alerts.filter_all_kinds") }, ...choices.kinds.map((kind) => ({ value: kind, label: t(`alert.kind_${kind}`) }))]}
        onChange={(value) => onChange("kind", value)}
        aria-label={t("alerts.filter_kind")}
      />
      <Select
        value={filter.machineID}
        options={[{ value: "", label: t("alerts.filter_all_machines") }, ...choices.machines.map((machine) => machineOption(t, { ...machine, kind: kinds.get(machine.id) }))]}
        onChange={(value) => onChange("machineID", value)}
        aria-label={t("alerts.filter_machine")}
      />
      <Select
        value={filter.serviceID}
        options={[{ value: "", label: t("alerts.filter_all_services") }, ...choices.serviceIDs.map((id) => ({ value: id, label: serviceNames.get(id) ?? id }))]}
        onChange={(value) => onChange("serviceID", value)}
        aria-label={t("alerts.filter_service")}
      />
      {isFiltering(filter) && (
        <Button small icon="x" onClick={onClear}>
          {t("alerts.filter_clear")}
        </Button>
      )}
    </div>
  );
}
