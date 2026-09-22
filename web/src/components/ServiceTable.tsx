import { useState } from "react";
import { Link } from "react-router";

import type { Service } from "../api/types";
import { useI18n } from "../i18n/context";
import { formatPorts, matchesFilter, type Filter } from "../lib/services";
import { formatBytes, formatDecimal } from "../lib/units";
import { updateAvailable, updateLabel } from "../lib/updates";
import { Pill, ServicePill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// Le filtre de la planche « Machine » : Tous, Actifs, Arrêtés.
const filters: Filter[] = ["all", "active", "stopped"];

export function ServiceFilter({ value, onChange }: { value: Filter; onChange: (filter: Filter) => void }) {
  const { t } = useI18n();
  return (
    <span className="seg" role="radiogroup" aria-label={t("services.col_state")}>
      {filters.map((filter) => (
        <label key={filter}>
          <input type="radio" name="service-filter" checked={value === filter} onChange={() => onChange(filter)} />
          <span>{t(`services.filter_${filter}`)}</span>
        </label>
      ))}
    </span>
  );
}

export function useServiceFilter(): [Filter, (filter: Filter) => void] {
  return useState<Filter>("all");
}

// Le tableau de la direction artistique : icône, nom et groupe, image en
// mono avec la pastille de mise à jour quand le registre en publie une,
// pastille d'état, domaine (les ports publiés en attendant les domaines),
// processeur et mémoire de la mesure courante, un tiret sans mesure.
export function ServiceTable({ services, withMachine }: { services: Service[]; withMachine: boolean }) {
  const { t, language } = useI18n();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("services.col_service")}</th>
          {withMachine && <th>{t("services.col_machine")}</th>}
          <th>{t("services.col_image")}</th>
          <th>{t("services.col_state")}</th>
          <th>{t("services.col_domain")}</th>
          <th className="th-num">{t("services.col_cpu")}</th>
          <th className="th-num th-end">{t("services.col_memory")}</th>
        </tr>
      </thead>
      <tbody>
        {services.map((service) => {
          const ports = formatPorts(service.ports);
          return (
            <tr key={service.id}>
              <td>
                <Link className="svc" to={`/services/${service.id}`}>
                  <Subject icon="box">
                    <span className="svc-name">
                      <span>{service.name}</span>
                      {service.group !== "" && service.group !== service.name && <span className="mono muted">{service.group}</span>}
                    </span>
                  </Subject>
                </Link>
              </td>
              {withMachine && (
                <td>
                  <Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>
                </td>
              )}
              <td className="num">
                <span className="image-cell">
                  {service.image}
                  {updateAvailable(service) && service.image_check !== null && (
                    <Pill tone="accent" dot={false}>
                      {updateLabel(t, service.image_check)}
                    </Pill>
                  )}
                </span>
              </td>
              <td>
                <ServicePill service={service} />
              </td>
              <td className="num">{ports !== "" ? ports : <span className="muted">{t("service.internal")}</span>}</td>
              <td className="num td-num">{service.current ? `${formatDecimal(language, service.current.cpu_percent, 0)} %` : "–"}</td>
              <td className="num td-num td-end">{service.current ? formatBytes(t, language, service.current.mem_used) : "–"}</td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}

export function filterServices(services: Service[], filter: Filter): Service[] {
  return services.filter((service) => matchesFilter(service, filter));
}
