import { Link } from "react-router";

import type { Container } from "../api/types";
import { useI18n } from "../i18n/context";
import { formatPublishedPorts, type Filter } from "../lib/containers";
import { formatBytes, formatDecimal } from "../lib/units";
import { updateAvailable, updateLabel } from "../lib/updates";
import { Pill, ContainerPill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// Le filtre de la planche « Machine » : Tous, Actifs, Arrêtés.
const filters: Filter[] = ["all", "active", "stopped"];

export function ContainerFilter({ value, onChange }: { value: Filter; onChange: (filter: Filter) => void }) {
  const { t } = useI18n();
  return (
    <span className="seg" role="radiogroup" aria-label={t("services.col_state")}>
      {filters.map((filter) => (
        <label key={filter}>
          <input type="radio" name="container-filter" checked={value === filter} onChange={() => onChange(filter)} />
          <span>{t(`services.filter_${filter}`)}</span>
        </label>
      ))}
    </span>
  );
}

// Le tableau de la direction artistique : icône, nom et groupe, image en
// mono avec la pastille de mise à jour quand le registre en publie une,
// pastille d'état, domaine (les ports publiés en attendant les domaines),
// processeur et mémoire de la mesure courante, un tiret sans mesure.
export function ContainerTable({ containers, withMachine }: { containers: Container[]; withMachine: boolean }) {
  const { t, language } = useI18n();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("services.col_container")}</th>
          {withMachine && <th className="wide-only">{t("services.col_machine")}</th>}
          <th className="wide-only">{t("services.col_image")}</th>
          <th>{t("services.col_state")}</th>
          <th className="wide-only">{t("services.col_exposure")}</th>
          <th className="th-num wide-only">{t("services.col_cpu")}</th>
          <th className="th-num th-end wide-only">{t("services.col_memory")}</th>
        </tr>
      </thead>
      <tbody>
        {containers.map((container) => {
          const ports = formatPublishedPorts(container.ports);
          return (
            <tr key={container.id}>
              <td>
                <Link className="svc" to={`/services/conteneurs/${container.id}`}>
                  <Subject icon="box">
                    <span className="svc-name">
                      <span>{container.name}</span>
                      {container.group !== "" && container.group !== container.name && <span className="mono muted">{container.group}</span>}
                    </span>
                  </Subject>
                </Link>
              </td>
              {withMachine && (
                <td className="wide-only">
                  <Link to={`/machines/${container.machine_id}`}>{container.machine_name}</Link>
                </td>
              )}
              <td className="num wide-only">
                <span className="image-cell">
                  {container.image}
                  {updateAvailable(container) && container.image_check !== null && (
                    <Pill tone="accent" dot={false}>
                      {updateLabel(t, container.image_check)}
                    </Pill>
                  )}
                </span>
              </td>
              <td>
                <ContainerPill container={container} />
              </td>
              <td className="num wide-only">{ports !== "" ? ports : <span className="muted">{t("container.internal")}</span>}</td>
              <td className="num td-num wide-only">{container.current ? `${formatDecimal(language, container.current.cpu_percent, 0)} %` : "–"}</td>
              <td className="num td-num td-end wide-only">{container.current ? formatBytes(t, language, container.current.mem_used) : "–"}</td>
            </tr>
          );
        })}
      </tbody>
    </Table>
  );
}
