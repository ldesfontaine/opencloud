import { Link } from "react-router";

import { useSSHKeyMatrix } from "../api/sshkeys";
import { Card, EmptyCard } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Table } from "../components/Table";
import { useT } from "../i18n/context";
import { buildMatrix, shortFingerprint, type Cell } from "../lib/sshkeys";
import "./SSHKeyMatrix.scss";

// Les clés SSH de toutes les machines d'un coup : une ligne par clé (par
// empreinte), une colonne par machine, et dans chaque cellule les comptes
// qui l'acceptent. Lecture seule : changer une clé reste un geste sur la
// machine. La matrice défile dans sa carte quand les machines sont nombreuses.
export function SSHKeyMatrix() {
  const t = useT();
  const matrix = useSSHKeyMatrix();
  const machines = matrix.data?.machines ?? [];
  const rows = buildMatrix(machines);
  return (
    <>
      <PageHead title={t("ssh.matrix_title")} subtitle={t("ssh.matrix_subtitle")} />
      {matrix.error && <Failure error={matrix.error} />}
      {matrix.data && rows.length === 0 && <EmptyCard icon="shield" text={t("ssh.matrix_empty_text")} />}
      {matrix.data && rows.length > 0 && (
        <Card className="scroll-x ssh-matrix">
          <Table>
            <thead>
              <tr>
                <th>{t("ssh.col_key")}</th>
                {machines.map((machine) => (
                  <th key={machine.machine_id}>
                    <Link to={`/machines/${machine.machine_id}/ssh`}>{machine.machine_name}</Link>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.fingerprint}>
                  <td>
                    <div className="ssh-key-id">
                      <span title={row.fingerprint}>
                        <span className="mono">{shortFingerprint(row.fingerprint)}</span> <span className="secondary mono">{`${row.type} ${row.bits}`}</span>
                        {row.weak && (
                          <>
                            {" "}
                            <span title={t("ssh.weak_hint")}>
                              <Pill tone="warn">{t("ssh.weak")}</Pill>
                            </span>
                          </>
                        )}
                      </span>
                      {row.comment !== "" && <span className="secondary">{row.comment}</span>}
                    </div>
                  </td>
                  {row.cells.map((cell, index) => (
                    <td key={machines[index]?.machine_id ?? index} className="num">
                      <CellView cell={cell} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  );
}

function CellView({ cell }: { cell: Cell }) {
  const t = useT();
  if (cell.kind === "accounts") {
    return <span>{cell.accounts.join(", ")}</span>;
  }
  if (cell.kind === "unknown") {
    return <span title={t("ssh.cell_unknown_hint")}>{t("ssh.cell_unknown")}</span>;
  }
  return <span className="secondary">{t("ssh.cell_none")}</span>;
}
