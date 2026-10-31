import { useState } from "react";
import { useNavigate } from "react-router";

import { useMachines } from "../../api/machines";
import { Button } from "../../components/Button";
import { Note } from "../../components/Card";
import { Modal } from "../../components/Modal";
import { Pill } from "../../components/Pill";
import { WizardBar } from "../../components/Wizard";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { enrollProgress, type EnrollProgress, type EnrollTarget } from "../../lib/enrollWait";
import type { TokenPageState } from "../../lib/machines";
import { formatRemaining } from "../../lib/time";
import { EnrollmentToken } from "./EnrollmentToken";

type Stage = "command" | "wait";

// Les deux dernières étapes d'un enrôlement, communes à l'ajout et au
// ré-enrôlement : la commande à coller sur la machine, puis l'attente de
// l'agent, qui se lit sur la liste des machines que le direct relit. Le
// jeton reste en mémoire dans cette fenêtre : il ne s'affiche qu'une fois.
// titles : les trois étapes de l'assistant ; la première est celle de
// l'appelant, déjà franchie.
export function EnrollFlow({
  title,
  titles,
  token,
  target,
  onClose,
}: {
  title: string;
  titles: string[];
  token: TokenPageState;
  target: EnrollTarget;
  onClose: () => void;
}) {
  const t = useT();
  const navigate = useNavigate();
  const now = useNow();
  const machines = useMachines();
  const [stage, setStage] = useState<Stage>("command");
  const progress = enrollProgress(target, machines.data?.machines ?? [], Date.parse(token.expires_at), now);

  const actions =
    stage === "command" ? (
      <Button variant="primary" onClick={() => setStage("wait")}>
        {t("wizard.next")}
      </Button>
    ) : (
      <>
        <Button onClick={() => setStage("command")}>{t("wizard.previous")}</Button>
        {progress.state === "connected" ? (
          <>
            <Button onClick={onClose}>{t("modal.close")}</Button>
            <Button variant="primary" icon="server" onClick={() => void navigate(`/machines/${progress.machine.id}`, { replace: true })}>
              {t("machine.open_sheet")}
            </Button>
          </>
        ) : (
          <Button variant="primary" onClick={onClose}>
            {t("modal.close")}
          </Button>
        )}
      </>
    );

  return (
    <Modal title={title} onClose={onClose} wide actions={actions}>
      <WizardBar titles={titles} position={stage === "command" ? 1 : 2} reached={stage === "command" ? 1 : 2} canJump={false} onJump={() => undefined} />
      {stage === "command" ? <EnrollmentToken token={token} /> : <AgentWait name={token.name} progress={progress} remaining={Date.parse(token.expires_at) - now} />}
    </Modal>
  );
}

// L'état de l'attente, annoncé aux lecteurs d'écran quand il change.
function AgentWait({ name, progress, remaining }: { name: string; progress: EnrollProgress; remaining: number }) {
  const t = useT();
  return (
    <div className="modal-form" role="status" aria-live="polite">
      {progress.state === "waiting" && (
        <>
          <Pill tone="neutral">{t("machine.wait_pending", name)}</Pill>
          <p className="secondary">{t("machine.wait_help", formatRemaining(t, remaining))}</p>
        </>
      )}
      {progress.state === "connected" && (
        <>
          <Pill tone="ok">{t("machine.wait_connected", progress.machine.name)}</Pill>
          <p className="secondary">{t("machine.wait_connected_help")}</p>
        </>
      )}
      {progress.state === "expired" && <Note tone="danger">{t("machine.wait_expired")}</Note>}
    </div>
  );
}
