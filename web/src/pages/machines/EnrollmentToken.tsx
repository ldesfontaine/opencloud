import { Note } from "../../components/Card";
import { Checklist } from "../../components/Checklist";
import { InstallSteps } from "../../components/InstallSteps";
import { TokenBox } from "../../components/Copy";
import { useChecklist } from "../../hooks/useChecklist";
import { useT } from "../../i18n/context";
import type { TokenPageState } from "../../lib/machines";
import { formatRemaining } from "../../lib/time";

// Le jeton d'une machine et les étapes à coller dessus. Il ne s'affiche
// qu'une fois, dans la commande de l'enrôlement : l'appelant tient la réponse
// en mémoire, jamais dans l'adresse ni dans l'historique du navigateur.
// checklist : les cases cochées des étapes, tenues par l'appelant.
export function EnrollmentToken({ token, checklist }: { token: TokenPageState; checklist: { done: readonly string[]; toggle: (id: string, checked: boolean) => void } }) {
  const t = useT();
  const expiresIn = formatRemaining(t, Date.parse(token.expires_at) - Date.now());
  return (
    <>
      <p className="secondary">{t("machine.token_subtitle", token.name)}</p>
      <Note tone="warn">{t("machine.token_once", expiresIn)}</Note>
      <InstallSteps steps={token.steps} produces={token.rights.some((right) => right.name === "backup.produce")} done={checklist.done} onToggle={checklist.toggle} />
      {token.proxmox_commands !== "" && <ProxmoxToken commands={token.proxmox_commands} />}
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
