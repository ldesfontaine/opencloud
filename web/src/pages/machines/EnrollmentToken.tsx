import { Note } from "../../components/Card";
import { Checklist } from "../../components/Checklist";
import { CopyButton, TokenBox } from "../../components/Copy";
import { Field } from "../../components/Field";
import { useChecklist } from "../../hooks/useChecklist";
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
      <Field label={t("machine.prepare_label")} help={t("machine.prepare_help")} tip={t("machine.prepare_tip")}>
        <TokenBox value={token.preparation} lines />
      </Field>
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
  const gestures = useChecklist();
  return (
    <div className="field">
      <span className="field-label">{t("machine.proxmox_token_title")}</span>
      <Checklist
        done={gestures.done}
        onToggle={gestures.toggle}
        items={[
          {
            id: "run",
            title: t("machine.proxmox_token_run_title"),
            content: (
              <>
                <p>{t("machine.proxmox_token_run_text")}</p>
                <TokenBox value={commands} lines />
              </>
            ),
          },
          { id: "secret", title: t("machine.proxmox_token_secret_title"), content: <p>{t("machine.proxmox_token_secret_text")}</p> },
        ]}
      />
      <Note tone="warn">{t("machine.capability_proxmox_warning")}</Note>
    </div>
  );
}
