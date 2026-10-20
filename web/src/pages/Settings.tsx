import { useAuth } from "../api/auth";
import { useMCP } from "../api/mcp";
import { useDiskSettings, useLifecycleSettings, useMailSettings } from "../api/settings";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { AccessSection } from "./settings/AccessSection";
import { DiskSection } from "./settings/DiskSection";
import { LifecycleSection } from "./settings/LifecycleSection";
import { MailSection } from "./settings/MailSection";
import { MCPSection } from "./settings/MCPSection";

// Paramètres : l'accès à l'interface, les seuils d'alerte du disque, le
// relais d'e-mails, la fin de support, puis l'accès d'un agent IA. Un secret ou un
// jeton ne s'affiche qu'une fois, à sa création, dans l'état de la page ;
// rechargée, elle ne le montre plus. Le titre reste affiché pendant que
// l'accès MCP se lit, ou s'il échoue.
export function Settings() {
  const t = useT();
  const mcp = useMCP();
  const access = useAuth();
  const disks = useDiskSettings();
  const mail = useMailSettings();
  const support = useLifecycleSettings();
  return (
    <>
      <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />
      <Loaded resource={mcp}>
        {(state) => (
          <>
            {access.data !== null && <AccessSection state={access.data} />}
            {disks.data !== null && <DiskSection settings={disks.data} reload={disks.reload} />}
            {mail.data !== null && <MailSection settings={mail.data} reload={mail.reload} />}
            {support.data !== null && <LifecycleSection settings={support.data} reload={support.reload} />}
            <MCPSection state={state} reload={mcp.reload} />
          </>
        )}
      </Loaded>
    </>
  );
}
