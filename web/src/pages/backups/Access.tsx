import { useState, type ReactNode } from "react";

import type { BackupSource } from "../../api/backups";
import { Button } from "../../components/Button";
import { Card, EmptyLine } from "../../components/Card";
import { TokenBox } from "../../components/Copy";
import { Icon, type IconName } from "../../components/Icon";
import { SectionHead } from "../../components/SectionHead";
import { Select } from "../../components/Select";
import { useT, type Translate } from "../../i18n/context";
import { browsePath } from "../../lib/backups";
import { guestArchiveName, repositoryUser, resticAccesses, restoreVMID, type ResticAccess } from "../../lib/backupOverview";

// « Accéder à mes sauvegardes » : le mot de passe du dépôt est sur la machine
// source (et scellé chez le serveur, qui ne s'en sert que pour la purge) ;
// l'explorateur ne tourne donc pas ici. L'écran donne les commandes à copier,
// à lancer sur la source, pour la sauvegarde choisie.
export function AccessSection({ sources }: { sources: readonly BackupSource[] }) {
  const t = useT();
  const accesses = resticAccesses(sources);
  const [chosen, setChosen] = useState("");
  const current = accesses.find((access) => access.key === chosen) ?? accesses[0];
  const picker =
    current === undefined ? undefined : (
      <span className="cluster">
        <Button icon="folder" to={browsePath(current.source.machine_id, current.protection.id)}>
          {t("backup.browse_in_app")}
        </Button>
        <Select
          aria-label={t("backup.access_pick")}
          icon="archive"
          value={current.key}
          options={accesses.map((access) => ({ value: access.key, label: access.source.name, hint: t("backup.access_pick_hint", access.source.machine_name, access.protection.target_machine_name) }))}
          onChange={setChosen}
        />
      </span>
    );
  return (
    <section className="stack" aria-labelledby="backup-access-title">
      <SectionHead id="backup-access-title" title={t("backup.access_title")} text={t("backup.access_text")} aside={picker} />
      {current === undefined ? <EmptyLine text={t("backup.access_none")} /> : <AccessCards access={current} />}
    </section>
  );
}

function AccessCards({ access }: { access: ResticAccess }) {
  const t = useT();
  const host = access.source.machine_name;
  const link = repositoryUser(access.protection.target_machine_id);
  const observed = access.source.protections.find((protection) => protection.origin === "observed");
  return (
    <>
      <div className="note note-neutral backup-access-note">
        <span className="backup-access-lead">
          <Icon name="lock" />
          <span>
            {t("backup.access_password_note", host)} <span className="mono">{t("backup.access_password_file", link)}</span> {t("backup.access_password_kept")}
          </span>
        </span>
        <span>{t("backup.access_run_from", host)}</span>
        <TokenBox value={t("backup.access_env", link)} />
      </div>
      <div className="grid-3">
        {access.protection.scope === "vm" ? (
          <HowCard icon="monitor" title={t("backup.access_restore_vm")}>
            <Step title={t("backup.access_restore_vm_new", host)}>
              <TokenBox value={vmRestoreCommand(t, access.source)} />
            </Step>
            <Step title={t("backup.access_restore_vm_swap")}>
              <p className="secondary">{t("backup.access_restore_vm_swap_text", restoreVMID(access.source.vmid))}</p>
            </Step>
            {observed !== undefined && <ProxmoxRestore host={host} storage={observed.archive_storage || observed.storage} />}
          </HowCard>
        ) : (
          <HowCard icon="box" title={t("backup.access_restore_service")}>
            <Step title={t("backup.access_restore_service_aside")}>
              <TokenBox value={t("backup.access_cmd_restore_all")} />
            </Step>
            <Step title={t("backup.access_restore_service_swap")}>
              <p className="secondary">{t("backup.access_restore_service_swap_text")}</p>
            </Step>
          </HowCard>
        )}
        {access.protection.scope === "service" && <PartRestores source={access.source} />}
      </div>
    </>
  );
}

// Une base et un volume sont chacun leur instantané : le dump ou l'archive se
// sort par son nom de fichier (database-<conteneur>.sql, volume-<nom>.tar),
// puis se rejoue dans le conteneur ou se défait dans un volume neuf.
function PartRestores({ source }: { source: BackupSource }) {
  const t = useT();
  const database = source.parts.find((part) => part.kind === "database");
  const volume = source.parts.find((part) => part.kind === "volume");
  const dumpFile = database === undefined ? "" : `database-${database.name}${database.engine === "mongo" ? ".archive" : ".sql"}`;
  return (
    <>
      {database !== undefined && (
        <HowCard icon="database" title={`${t("backup.access_restore_database")} · ${database.name}`}>
          <Step title={t("backup.access_restore_database_dump")}>
            <TokenBox value={dumpCommand(t, dumpFile)} />
          </Step>
          <Step title={t("backup.access_restore_database_replay")}>
            <TokenBox value={t(`backup.access_cmd_replay_${database.engine}`, database.name, dumpFile)} />
          </Step>
        </HowCard>
      )}
      {volume !== undefined && (
        <HowCard icon="disk" title={`${t("backup.access_restore_volume")} · ${volume.name}`}>
          <Step title={t("backup.access_restore_volume_dump")}>
            <TokenBox value={dumpCommand(t, `volume-${volume.name}.tar`)} />
          </Step>
          <Step title={t("backup.access_restore_volume_unpack")}>
            <TokenBox value={t("backup.access_cmd_unpack_volume", `${volume.name}-restauration`, `volume-${volume.name}.tar`)} />
          </Step>
        </HowCard>
      )}
    </>
  );
}

function dumpCommand(t: Translate, file: string): string {
  return t("backup.access_cmd_dump_part", file, file, file);
}

function vmRestoreCommand(t: Translate, source: BackupSource): string {
  const key = source.kind === "lxc" ? "backup.access_cmd_restore_lxc" : "backup.access_cmd_restore_vm";
  return t(key, guestArchiveName(source), restoreVMID(source.vmid));
}

function HowCard({ icon, title, children }: { icon: IconName; title: string; children: ReactNode }) {
  return (
    <Card className="backup-how">
      <h3>
        <span className="backup-badge" aria-hidden="true">
          <Icon name={icon} />
        </span>
        {title}
      </h3>
      <ol className="backup-steps">{children}</ol>
    </Card>
  );
}

function Step({ title, children }: { title: string; children: ReactNode }) {
  return (
    <li>
      <b>{title}</b>
      {children}
    </li>
  );
}

// La même VM, sauvegardée par Proxmox de lui-même : elle se restaure dans son
// interface, par ses libellés.
function ProxmoxRestore({ host, storage }: { host: string; storage: string }) {
  const t = useT();
  return (
    <div className="backup-alt">
      <b>{t("backup.access_observed")}</b>
      <ol className="backup-crumb" aria-label={t("backup.access_observed")}>
        <li>{host}</li>
        <li>{storage}</li>
        <li>{t("backup.access_pve_backups")}</li>
        <li>{t("backup.access_pve_restore")}</li>
      </ol>
      <span className="secondary">{t("backup.access_observed_text")}</span>
    </div>
  );
}
