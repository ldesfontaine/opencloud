import { useEffect } from "react";

import type { CertificateInspection } from "../api/certificate";
import type { CertificateInspector } from "../hooks/useCertificateInspector";
import { useT } from "../i18n/context";
import { formatFingerprint, sameFingerprint } from "../lib/fingerprint";
import { Button } from "./Button";
import { Note } from "./Card";
import { Field, Input } from "./Field";
import { Fold } from "./Disclosure";
import { Failure } from "./Failure";
import { CopyButton } from "./Copy";
import "./CertificateCheck.scss";

// Ce que l'adresse a répondu, en une phrase : joignable et reconnue, joignable
// mais à confirmer, ou injoignable avec la cause la plus probable.
function ReportNote({ report }: { report: CertificateInspection }) {
  const t = useT();
  if (report.outcome === "unreachable") {
    return <Note tone="danger">{t(`certificate.cause_${report.cause === "" ? "other" : report.cause}`)}</Note>;
  }
  return report.outcome === "trusted" ? <Note tone="ok">{t("certificate.result_trusted")}</Note> : <Note tone="warn">{t("certificate.result_untrusted")}</Note>;
}

// « Tester l'accès » : l'étape de l'adresse essaie la connexion depuis le
// serveur, sans clé, et dit le résultat sous le champ.
export function AddressCheck({ inspector, isAddressValid }: { inspector: CertificateInspector; isAddressValid: boolean }) {
  const t = useT();
  return (
    <div className="certificate-check">
      <div>
        <Button onClick={inspector.inspect} disabled={!isAddressValid || inspector.busy}>
          {inspector.busy ? t("certificate.testing") : t("certificate.test")}
        </Button>
      </div>
      <Failure error={inspector.error} />
      {inspector.report !== null && !inspector.busy && <ReportNote report={inspector.report} />}
    </div>
  );
}

// L'étape du certificat : le serveur relève l'empreinte de l'adresse, l'affiche,
// et l'opérateur confirme d'un clic. La saisie à la main reste le repli.
// explanation : la phrase du produit (« c'est la carte d'identité de votre
// OPNsense… »), déjà traduite.
export function CertificateFingerprint({
  inspector,
  pin,
  onPin,
  explanation,
  fieldId,
}: {
  inspector: CertificateInspector;
  pin: string;
  onPin: (pin: string) => void;
  explanation: string;
  fieldId: string;
}) {
  const t = useT();
  const { report, busy, inspect } = inspector;

  // Arriver sur l'étape sans relevé de cette adresse (le test n'a pas été
  // pressé) le fait tout seul, une fois.
  useEffect(() => {
    if (report === null && !busy && inspector.error === null) {
      inspect();
    }
    // Une fois à l'arrivée : un échec ne se relance que par « Réessayer ».
  }, []);

  const manualField = (
    <Field label={t("certificate.pin_label")} htmlFor={fieldId} help={t("certificate.manual_help")}>
      <Input id={fieldId} mono value={pin} onChange={(event) => onPin(event.target.value)} autoComplete="off" />
    </Field>
  );

  if (report === null) {
    return (
      <div className="certificate-check">
        <p className="secondary">{busy ? t("certificate.reading") : t("certificate.not_read")}</p>
        <Failure error={inspector.error} />
        {!busy && <div><Button onClick={inspect}>{t("certificate.retry")}</Button></div>}
        {manualField}
      </div>
    );
  }
  if (report.outcome === "unreachable") {
    return (
      <div className="certificate-check">
        <ReportNote report={report} />
        <div><Button onClick={inspect} disabled={busy}>{t("certificate.retry")}</Button></div>
        {manualField}
      </div>
    );
  }
  if (report.outcome === "trusted") {
    return (
      <div className="certificate-check">
        <Note tone="ok">{t("certificate.trusted_nothing_to_do")}</Note>
        <Fold label={t("certificate.manual")} initiallyOpen={pin !== ""}>
          {manualField}
        </Fold>
      </div>
    );
  }
  const isConfirmed = sameFingerprint(pin, report.fingerprint);
  const displayed = formatFingerprint(report.fingerprint);
  return (
    <div className="certificate-check">
      <p>{explanation}</p>
      <div className="token-box">
        <code className="mono" aria-label={t("certificate.fingerprint_label")}>{displayed}</code>
        <CopyButton text={displayed} />
      </div>
      {pin !== "" && !isConfirmed && <Note tone="warn">{t("certificate.pin_differs")}</Note>}
      {isConfirmed ? (
        <Note tone="ok">{t("certificate.confirmed")}</Note>
      ) : (
        <div>
          <Button variant="primary" icon="check" onClick={() => onPin(report.fingerprint)}>
            {t("certificate.confirm")}
          </Button>
        </div>
      )}
      <Fold label={t("certificate.manual")} initiallyOpen={pin !== "" && !isConfirmed}>
        {manualField}
      </Fold>
    </div>
  );
}
