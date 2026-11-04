import { useState } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router";

import { reenrollMachine, useMachines, type Machine } from "../../api/machines";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Modal, ModalCancel } from "../../components/Modal";
import { RightsFields } from "../../components/RightsFields";
import { WizardBar } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { anchorFieldsOf, anchorsOf, emptyAnchorFields } from "../../lib/anchors";
import { capabilitiesOf, isRightsModified, rightsFormOf, tokenPageState, type RightsValues, type TokenPageState } from "../../lib/machines";
import { detectedProxmox } from "../../lib/proxmox";
import { wantedRightOf, withWantedRight } from "../../lib/rights";
import { EnrollFlow } from "./EnrollFlow";

const noRights: RightsValues = { rights: [], backupPath: "", writePaths: "", resticPath: "", anchors: emptyAnchorFields };

// « Ré-enrôler » : la fenêtre de la fiche d'une machine, à l'adresse
// /machines/:id/reenroller. Ré-enrôler est aussi la seule façon de changer les
// droits d'un agent (décision 10) : 1. les droits, cochés comme le jeton de
// l'agent courant les demandait ; 2. les étapes à coller ; 3. l'attente du
// nouvel agent, qui remplace l'ancien (EnrollFlow). Un refus de droit y mène
// avec ?droit=<nom>[&chemin=<dossier>] : ce droit est coché d'avance.
export function ReenrollMachine() {
  const { id = "" } = useParams();
  const machines = useMachines();
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (machines.data && !current) {
    return <Navigate to="/machines" replace />;
  }
  return current ? <ReenrollDialog machine={current} /> : null;
}

function ReenrollDialog({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const requested = machine.agent?.requested ?? [];
  const [search] = useSearchParams();
  const wanted = wantedRightOf(search);
  const heldAnchors = anchorFieldsOf(machine.agent?.anchors);
  const form = useForm(noRights, { source: requested, fill: (source) => withWantedRight(rightsFormOf(source, heldAnchors), wanted) });
  const { values } = form;
  const action = useAction();
  const [token, setToken] = useState<TokenPageState | null>(null);
  // L'agent d'avant, retenu à la création du jeton : celui qui arrive en diffère.
  const [previousAgentID, setPreviousAgentID] = useState<string | null>(null);
  const close = () => void navigate(`/machines/${machine.id}`, { replace: true });
  const titles = [t("machine.step_rights"), t("machine.step_install"), t("machine.step_wait")];
  const title = t("machine.reenroll_title", machine.name);

  if (token !== null) {
    return <EnrollFlow title={title} titles={titles} token={token} target={{ kind: "reenroll", machineID: machine.id, previousAgentID }} onClose={close} />;
  }

  const create = () => {
    const rights = capabilitiesOf(values);
    action.run(() => reenrollMachine(machine.id, rights, anchorsOf(values.anchors, values.rights)), {
      topic: "all",
      onDone: (created) => {
        setPreviousAgentID(machine.agent?.id ?? null);
        setToken(tokenPageState(created, rights));
      },
    });
  };

  return (
    <Modal
      title={title}
      onClose={close}
      dirty={form.isFilled && isRightsModified(values, withWantedRight(rightsFormOf(requested, heldAnchors), wanted))}
      wide
      actions={
        <>
          <ModalCancel />
          <Button variant="primary" icon="refresh" disabled={action.busy || !form.isFilled} onClick={create}>
            {t("machine.rights_submit")}
          </Button>
        </>
      }
    >
      <WizardBar titles={titles} position={0} reached={0} canJump={false} onJump={() => undefined} />
      <p className="secondary">{t("machine.rights_subtitle")}</p>
      <RightsFields values={values} update={form.update} proxmoxDetected={machine.role === "proxmox" || detectedProxmox(machine) !== null} />
      <Failure error={action.error} />
    </Modal>
  );
}
