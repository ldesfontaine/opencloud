import { Fragment } from "react";

import type { BackupMachine } from "../../api/backups";
import { CopyButton } from "../../components/Copy";
import { ExternalLink } from "../../components/ExternalLink";
import { useT } from "../../i18n/context";
import { installPlan, restServerVersion, type MissingSoftware } from "../../lib/resticInstall";

// Ce qu'il faut installer, sur quelle machine : son nom en tête, puis les
// commandes à copier une à une, chacune marquée de la machine où la coller.
// Sans commande sûre (une autre architecture, un autre système), la page des
// versions. framed : dans une fenêtre, le bloc a son cadre ; sur une carte,
// un trait le sépare.
export function InstallBlock({ software, machine, framed = false }: { software: MissingSoftware; machine: Pick<BackupMachine, "machine_name" | "os" | "arch">; framed?: boolean }) {
  const t = useT();
  const plan = installPlan(software, machine);
  const host = machine.machine_name;
  const arch = machine.arch === "" ? t("backup.install_unknown") : machine.arch;
  const os = machine.os === "" ? t("backup.install_unknown") : machine.os;
  const detail = software === "rest-server" ? t("backup.install_rest_server_detail", arch, restServerVersion, host) : t("backup.install_restic_detail", os, host);
  return (
    <div className={framed ? "backup-install backup-install-framed" : "backup-install"} aria-label={t("backup.install_title", software, host)} role="group">
      <div className="backup-install-head">
        <b>{t("backup.install_title", software, host)}</b>
        <span className="secondary">{detail}</span>
      </div>
      {plan.page === null ? (
        <ol className="backup-steps">
          {plan.steps.map((step) => (
            <li key={step.command}>
              <b>{t(step.title)}</b>
              <div className="token-box">
                <span className="backup-on">{host}</span>
                <code className="mono">
                  <CommandText command={step.command} />
                </code>
                <CopyButton text={step.command} />
              </div>
            </li>
          ))}
        </ol>
      ) : (
        <p className="backup-install-page">
          <span className="secondary">{software === "rest-server" ? t("backup.install_no_arch", arch) : t("backup.install_no_package", os)}</span>
          <ExternalLink href={plan.page}>{t("backup.install_releases", software)}</ExternalLink>
        </p>
      )}
    </div>
  );
}

// Une longue commande passe à la ligne après un « / » ou entre deux mots,
// jamais sur le tiret de « rest-server » ; la copie reste la commande telle
// quelle.
function CommandText({ command }: { command: string }) {
  const words = command.split(" ");
  return words.map((word, wordIndex) => (
    <Fragment key={wordIndex}>
      {wordIndex > 0 && " "}
      {word.split("/").map((segment, segmentIndex) => (
        <Fragment key={segmentIndex}>
          {segmentIndex > 0 && (
            <>
              /<wbr />
            </>
          )}
          <span className="backup-nb">{segment}</span>
        </Fragment>
      ))}
    </Fragment>
  ));
}
