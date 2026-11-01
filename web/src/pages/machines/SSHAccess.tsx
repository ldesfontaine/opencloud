import { useState } from "react";
import { Link } from "react-router";

import { useSSHKeyMatrix } from "../../api/sshkeys";
import { Card, CardBody, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { Table } from "../../components/Table";
import { Verdict } from "../../components/Verdict";
import { useNow } from "../../hooks/useNow";
import { useT, type Translate } from "../../i18n/context";
import { buildKeyAccess, buildMachineAccess, filterKeys, keyFilters, summarizeAccess, type AccessSummary, type KeyAccess, type KeyFilter, type MachineAccess } from "../../lib/sshAccess";
import { shortFingerprint } from "../../lib/sshkeys";
import { formatWhen } from "../../lib/time";
import "./SSHAccess.scss";

type Reading = "keys" | "machines";

const readings: Reading[] = ["keys", "machines"];

// L'onglet « Accès SSH » de Machines : qui peut se connecter à quoi. Deux
// lectures des mêmes relevés, par clé (où entre-t-elle ?) et par machine (qui
// entre chez elle ?). Lecture seule : retirer une clé reste un geste sur la
// machine, hors d'openCloud.
export function SSHAccess() {
  const t = useT();
  const matrix = useSSHKeyMatrix();
  const [reading, setReading] = useState<Reading>("keys");
  const [filter, setFilter] = useState<KeyFilter>("all");
  const reports = matrix.data?.machines ?? [];
  const keys = buildKeyAccess(reports);
  const shown = filterKeys(keys, filter);
  return (
    <>
      {matrix.error && <Failure error={matrix.error} />}
      {matrix.data && keys.length === 0 && <EmptyCard icon="shield" text={t("ssh.access_empty")} />}
      {matrix.data && keys.length > 0 && (
        <>
          <AccessVerdict summary={summarizeAccess(keys, reports)} />
          <div className="ssh-access-bar">
            <Choice label={t("ssh.access_view")} name="ssh-reading" choices={readings} value={reading} text={(choice) => t(`ssh.view_${choice}`)} onChange={setReading} />
            {reading === "keys" && <Choice label={t("ssh.access_filter")} name="ssh-filter" choices={keyFilters} value={filter} text={(choice) => t(`ssh.filter_${choice}`)} onChange={setFilter} />}
          </div>
          {reading === "keys" ? <KeyTable rows={shown} /> : <MachineTable machines={buildMachineAccess(reports)} />}
        </>
      )}
    </>
  );
}

// Est-ce que ça va ? Une clé faible qui ouvre encore une machine, ou un
// fichier que l'agent refuse de lire, demande l'opérateur ; sinon le verdict
// reste blanc. Une machine lue en partie (compte dédié) n'est pas une alarme :
// une ligne grise dit comment tout lire.
function AccessVerdict({ summary }: { summary: AccessSummary }) {
  const t = useT();
  const watch = summary.weak > 0 || summary.incomplete > 0;
  return (
    <Verdict
      tone={watch ? "warn" : "ok"}
      pill={<Pill tone={watch ? "warn" : "ok"}>{t(watch ? "ssh.access_verdict_watch" : "ssh.access_verdict_ok")}</Pill>}
      fact={verdictFact(t, summary)}
      context={verdictContext(t, summary)}
    >
      {t("ssh.access_counts", summary.keys, summary.privileged, summary.single)}
    </Verdict>
  );
}

function verdictContext(t: Translate, summary: AccessSummary): string | null {
  if (summary.weak > 0 && summary.incomplete > 0) {
    return t("ssh.access_context_incomplete");
  }
  return summary.partial > 0 ? t("ssh.access_context_partial") : null;
}

function verdictFact(t: Translate, summary: AccessSummary): string {
  if (summary.weak > 0) {
    return summary.weak === 1 ? t("ssh.access_fact_weak_one", summary.weak) : t("ssh.access_fact_weak", summary.weak);
  }
  if (summary.incomplete > 0) {
    return summary.incomplete === 1 ? t("ssh.access_fact_incomplete_one", summary.incomplete) : t("ssh.access_fact_incomplete", summary.incomplete);
  }
  if (summary.partial > 0) {
    return summary.partial === 1 ? t("ssh.access_fact_partial_one", summary.partial) : t("ssh.access_fact_partial", summary.partial);
  }
  return t("ssh.access_fact_ok");
}

// Un choix parmi quelques-uns : le segment de la direction artistique.
function Choice<Value extends string>({
  label,
  name,
  choices,
  value,
  text,
  onChange,
}: {
  label: string;
  name: string;
  choices: readonly Value[];
  value: Value;
  text: (choice: Value) => string;
  onChange: (choice: Value) => void;
}) {
  return (
    <span className="seg" role="radiogroup" aria-label={label}>
      {choices.map((choice) => (
        <label key={choice}>
          <input type="radio" name={name} checked={value === choice} onChange={() => onChange(choice)} />
          <span>{text(choice)}</span>
        </label>
      ))}
    </span>
  );
}

function KeyTable({ rows }: { rows: KeyAccess[] }) {
  const t = useT();
  if (rows.length === 0) {
    return <EmptyCard icon="shield" text={t("ssh.filter_empty")} />;
  }
  return (
    <Card className="scroll-x ssh-access">
      <Table>
        <thead>
          <tr>
            <th>{t("ssh.col_fingerprint")}</th>
            <th>{t("ssh.col_grants")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.fingerprint}>
              <td>
                <div className="ssh-access-key">
                  <b title={row.fingerprint}>{row.comment === "" ? t("ssh.no_comment") : row.comment}</b>
                  <span className="secondary mono">
                    {`${row.type} ${row.bits} · ${shortFingerprint(row.fingerprint)}`}
                    {row.weak && (
                      <>
                        {" "}
                        <span title={t("ssh.weak_hint")}>
                          <Pill tone="warn">{t("ssh.weak")}</Pill>
                        </span>
                      </>
                    )}
                  </span>
                </div>
              </td>
              <td>
                <div className="ssh-access-grants">
                  {row.grants.map((grant) => (
                    <div className="ssh-access-grant" key={grant.machineID}>
                      <Link to={`/machines/${grant.machineID}/ssh`}>{grant.machineName}</Link>
                      {grant.accounts.map((account) => (
                        <Account key={account.name} name={account.name} privileged={account.privileged} />
                      ))}
                    </div>
                  ))}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </Table>
      <CardBody gap={8}>
        <span className="secondary">{t("ssh.access_note")}</span>
      </CardBody>
    </Card>
  );
}

function MachineTable({ machines }: { machines: MachineAccess[] }) {
  const t = useT();
  const now = useNow();
  return (
    <Card className="scroll-x ssh-access">
      <Table>
        <thead>
          <tr>
            <th>{t("ssh.col_account")}</th>
            <th>{t("ssh.col_keys")}</th>
            <th>{t("ssh.col_read")}</th>
          </tr>
        </thead>
        <tbody>
          {machines.flatMap((machine) => [
            <tr className="ssh-access-machine" key={machine.machineID}>
              <td colSpan={3}>
                <Link to={`/machines/${machine.machineID}/ssh`}>{machine.machineName}</Link>{" "}
                <span className="secondary">{machineReading(t, machine, now)}</span>
              </td>
            </tr>,
            ...machine.accounts.map((account) => (
              <tr key={`${machine.machineID}/${account.name}`}>
                <td>
                  <Account name={account.name} privileged={account.privileged} />
                </td>
                <td>
                  {account.keys.length === 0 ? (
                    <span className="secondary">{t(`ssh.state_${account.state}`)}</span>
                  ) : (
                    account.keys.map((key, index) => (
                      <span key={key.fingerprint} title={key.fingerprint}>
                        {index > 0 && ", "}
                        {key.comment === "" ? t("ssh.no_comment") : key.comment}
                        {key.weak && (
                          <>
                            {" "}
                            <Pill tone="warn">{t("ssh.weak")}</Pill>
                          </>
                        )}
                      </span>
                    ))
                  )}
                </td>
                <td>
                  <Pill tone={account.state === "read" ? "ok" : "warn"}>{t(`ssh.state_${account.state}`)}</Pill>
                </td>
              </tr>
            )),
          ])}
        </tbody>
      </Table>
    </Card>
  );
}

// Quand l'agent a relevé la machine, et si on l'a lue en entier.
function machineReading(t: Translate, machine: MachineAccess, now: number): string {
  if (machine.reportedAt === null) {
    return t("ssh.never_reported");
  }
  const when = t("ssh.reported", formatWhen(machine.reportedAt, now));
  return machine.complete ? when : `${when} · ${t("ssh.partial")}`;
}

// Un compte, root en rouge : c'est lui que l'opérateur cherche.
function Account({ name, privileged }: { name: string; privileged: boolean }) {
  const t = useT();
  return (
    <span className={privileged ? "ssh-account ssh-account-root" : "ssh-account"} title={privileged ? t("ssh.root_hint") : undefined}>
      {name}
    </span>
  );
}
