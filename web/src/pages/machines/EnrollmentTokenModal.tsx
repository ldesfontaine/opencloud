import { useEffect, useState } from "react";
import { Navigate, useLocation, useNavigate } from "react-router";

import { Button } from "../../components/Button";
import { Modal } from "../../components/Modal";
import { useT } from "../../i18n/context";
import type { TokenPageState } from "../../lib/machines";
import { EnrollmentToken } from "./EnrollmentToken";

// Le jeton d'un ré-enrôlement, à l'adresse /machines/jeton : la fiche de la
// machine y mène avec le jeton dans l'état de navigation, qui en sort aussitôt
// (le navigateur garde cet état au rechargement et au retour arrière, et le
// jeton ne doit s'afficher qu'une fois). Rechargée, la fenêtre n'a plus rien à
// montrer et renvoie aux machines. C'est voulu. L'ajout d'une machine garde
// son jeton en mémoire dans AddMachine.
export function EnrollmentTokenModal() {
  const t = useT();
  const location = useLocation();
  const navigate = useNavigate();
  const [token] = useState(location.state as TokenPageState | null);
  useEffect(() => {
    if (location.state !== null) {
      void navigate(location.pathname, { replace: true, state: null });
    }
  }, [location, navigate]);
  if (!token) {
    return <Navigate to="/machines" replace />;
  }
  const close = () => void navigate("/machines", { replace: true });
  return (
    <Modal
      title={t("machine.token_title")}
      onClose={close}
      wide
      actions={
        <Button variant="primary" onClick={close}>
          {t("modal.close")}
        </Button>
      }
    >
      <EnrollmentToken token={token} />
    </Modal>
  );
}
