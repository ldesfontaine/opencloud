import { useMachineSSHKeys, type SSHAccount, type SSHKey, type SSHState } from "../api/sshkeys";
import type { Machine } from "../api/machines";
import { useNow } from "../hooks/useNow";
import { useT, type Translate } from "../i18n/context";
import { describeAccount } from "../lib/machines";
import { hasDenied, isWeak, shortFingerprint } from "../lib/sshkeys";
import { formatWhen } from "../lib/time";
import type { Tone } from "../lib/tone";
import { Card, CardBody, CardHeader, Note } from "./Card";
import { Failure } from "./Failure";
import { Pill } from "./Pill";
import { Table } from "./Table";
import "./MachineSSHKeys.scss";

const stateTones: Record<SSHState, Tone> = { read: "ok", none: "neutral", denied: "warn", unsafe: "warn", too_large: "warn" };

// L'onglet « Clés SSH » d'une machine : pour chaque compte, l'état de la
// lecture et les clés que l'agent y a trouvées. Lecture seule : ni bouton,
// ni formulaire. Le direct relit sur le sujet des machines.
export function MachineSSHKeys({ machine }: { machine: Machine }) {
  const t = useT();
  const now = useNow();
  const keys = useMachineSSHKeys(machine.id);
  const report = keys.data;
  if (keys.error && !report) {
    return <Failure error={keys.error} />;
  }
  if (!report) {
    return null;
  }
  const agentAccount = describeAccount(machine.agent?.run_as ?? null);
  return (
    <div className="ssh-keys">
      {keys.error && <Failure error={keys.error} />}
      <Card>
        <CardHeader title={t("ssh.machine_title")} />
        <CardBody gap={8}>
          <span className="secondary">{report.reported_at === null ? t("ssh.never_reported") : t("ssh.reported", formatWhen(report.reported_at, now))}</span>
          {report.truncated && <Note tone="warn">{t("ssh.truncated_report")}</Note>}
          {hasDenied(report) && <Note tone="warn">{t("ssh.dedicated_account")}</Note>}
        </CardBody>
      </Card>
      {report.accounts.map((account) => (
        <AccountCard key={account.name} account={account} agentAccount={agentAccount} />
      ))}
    </div>
  );
}

function AccountCard({ account, agentAccount }: { account: SSHAccount; agentAccount: string | null }) {
  const t = useT();
  return (
    <Card className="scroll-x">
      <CardHeader title={account.name} aside={<AccountState state={account.state} uid={account.uid} />} />
      {account.keys.length > 0 ? (
        <Table>
          <KeyHead />
          <tbody>
            {account.keys.map((key) => (
              <KeyRow key={`${key.file}/${key.fingerprint}`} sshKey={key} />
            ))}
          </tbody>
        </Table>
      ) : (
        <CardBody gap={8}>
          <span className="secondary">{stateSentence(t, account.state, agentAccount)}</span>
        </CardBody>
      )}
      {(account.truncated || account.ignored > 0 || (account.keys.length > 0 && account.state !== "read")) && (
        <CardBody gap={8}>
          {account.keys.length > 0 && account.state !== "read" && <span className="secondary">{stateSentence(t, account.state, agentAccount)}</span>}
          {account.truncated && <span className="secondary">{t("ssh.truncated_account")}</span>}
          {account.ignored > 0 && <span className="secondary">{t("ssh.ignored", account.ignored)}</span>}
        </CardBody>
      )}
    </Card>
  );
}

// La phrase de ce que l'état veut dire pour le compte, quand il n'a pas de clé à montrer.
function stateSentence(t: Translate, state: SSHState, agentAccount: string | null): string {
  switch (state) {
    case "denied":
      return agentAccount === null ? t("ssh.denied_unknown") : t("ssh.denied_account", agentAccount);
    case "unsafe":
      return t("ssh.unsafe_note");
    case "too_large":
      return t("ssh.too_large_note");
    case "none":
      return t("ssh.none_note");
    default:
      return t("ssh.no_keys");
  }
}

export function AccountState({ state, uid }: { state: SSHState; uid: number }) {
  const t = useT();
  return (
    <span className="ssh-state">
      <span className="secondary mono">{t("ssh.account_uid", uid)}</span>
      <Pill tone={stateTones[state]}>{t(`ssh.state_${state}`)}</Pill>
    </span>
  );
}

function KeyHead() {
  const t = useT();
  return (
    <thead>
      <tr>
        <th>{t("ssh.col_type")}</th>
        <th>{t("ssh.col_bits")}</th>
        <th>{t("ssh.col_fingerprint")}</th>
        <th>{t("ssh.col_comment")}</th>
        <th>{t("ssh.col_options")}</th>
        <th>{t("ssh.col_file")}</th>
      </tr>
    </thead>
  );
}

function KeyRow({ sshKey }: { sshKey: SSHKey }) {
  const t = useT();
  return (
    <tr>
      <td className="num">{sshKey.type}</td>
      <td className="num">
        {sshKey.bits}
        {isWeak(sshKey) && (
          <>
            {" "}
            <span title={t("ssh.weak_hint")}>
              <Pill tone="warn">{t("ssh.weak")}</Pill>
            </span>
          </>
        )}
      </td>
      <td className="num" title={sshKey.fingerprint}>
        {shortFingerprint(sshKey.fingerprint)}
      </td>
      <td>{sshKey.comment}</td>
      <td className="num">{sshKey.options.map((option) => (option === "other" ? t("ssh.option_other") : option)).join(", ")}</td>
      <td className="num">{sshKey.file}</td>
    </tr>
  );
}
