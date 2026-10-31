import { Note } from "../../components/Card";
import { CopyButton, TokenBox } from "../../components/Copy";
import { useT } from "../../i18n/context";
import { unitDropIn, unitWritePaths, type TokenPageState } from "../../lib/machines";
import { formatRemaining } from "../../lib/time";

// Le jeton d'une machine et la commande à coller dessus. Il ne s'affiche
// qu'une fois : l'appelant le tient en mémoire, jamais dans l'adresse ni dans
// l'historique du navigateur.
export function EnrollmentToken({ token }: { token: TokenPageState }) {
  const t = useT();
  // Un droit qui écrit hors de l'état de l'agent demande un drop-in de son unité.
  const rights = token.rights ?? [];
  const writePaths = unitWritePaths(rights);
  const expiresIn = formatRemaining(t, Date.parse(token.expires_at) - Date.now());
  return (
    <>
      <p className="secondary">{t("machine.token_subtitle", token.name)}</p>
      <Note tone="warn">{t("machine.token_once", expiresIn)}</Note>
      <div className="field">
        <span className="field-label">{t("machine.token_label")}</span>
        <TokenBox value={token.token} />
      </div>
      <div className="field">
        <span className="field-label">{t("machine.prepare_label")}</span>
        <TokenBox value={token.preparation} lines />
        <span className="field-help">{t("machine.prepare_help")}</span>
      </div>
      <Note tone="warn">{t("machine.prepare_docker_note")}</Note>
      {rights.some((right) => right.name === "backup.produce") && <Note tone="warn">{t("machine.capability_produce_warning")}</Note>}
      <div className="field">
        <span className="field-label">{t("machine.command_label")}</span>
        <TokenBox value={token.command} />
      </div>
      {token.proxmox_commands !== "" && <ProxmoxToken commands={token.proxmox_commands} />}
      {writePaths.length > 0 && (
        <Note tone="warn">
          <p>{t("machine.unit_read_write", writePaths.join(", "))}</p>
          <div className="token-box">
            <pre className="mono">{unitDropIn(writePaths)}</pre>
            <CopyButton text={unitDropIn(writePaths)} />
          </div>
        </Note>
      )}
      {token.url_local && <Note tone="danger">{t("machine.url_local")}</Note>}
    </>
  );
}

// Les droits proxmox.* passent par un jeton Proxmox local que l'opérateur crée
// lui-même sur l'hôte : le secret ne quitte jamais l'hôte.
function ProxmoxToken({ commands }: { commands: string }) {
  const t = useT();
  return (
    <div className="field">
      <span className="field-label">{t("machine.proxmox_token_title")}</span>
      <span className="field-help">{t("machine.proxmox_token_help")}</span>
      <TokenBox value={commands} lines />
      <Note tone="warn">{t("machine.capability_proxmox_warning")}</Note>
    </div>
  );
}
