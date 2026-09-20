import { Link } from "react-router";

import type { Certificate, Probe } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useCertificateThresholds, useT, type Translate } from "../i18n/context";
import { read, type CertificateReading } from "../lib/certificates";
import { formatDuration } from "../lib/time";
import { Card, CardBody, CardHeader, Empty } from "./Card";
import { Pill } from "./Pill";
import "./Certificate.scss";

// Ce qui reste, en jours, dans le ton de son urgence. Un certificat expiré
// dit depuis combien de temps plutôt qu'un nombre négatif.
export function Remaining({ reading }: { reading: CertificateReading }) {
  const t = useT();
  const text = reading.days < 0 ? t("cert.expired_days", -reading.days) : t("cert.days", reading.days);
  return <span className={`mono nowrap cert-days ${reading.tone === "ok" ? "secondary" : reading.tone}`}>{text}</span>;
}

// L'état d'un certificat, dans le vocabulaire de la direction artistique.
// Il ne parle que de l'échéance.
export function CertificatePill({ reading }: { reading: CertificateReading }) {
  const t = useT();
  return <Pill tone={reading.tone}>{t(`cert.state_${reading.state}`)}</Pill>;
}

// La confiance, à côté de l'échéance et jamais à sa place : une chaîne
// qu'on ne peut pas vérifier ne rend pas la date fausse. Rien à dire quand
// tout se vérifie.
export function TrustPill({ reading }: { reading: CertificateReading }) {
  const t = useT();
  if (reading.revoked) {
    return <Pill tone="danger">{t("cert.trust_revoked")}</Pill>;
  }
  if (reading.untrusted) {
    return <Pill tone="warn">{t("cert.trust_unverified")}</Pill>;
  }
  return null;
}

// La note d'une ligne : ce qu'on ne peut pas prouver d'abord, l'âge du
// certificat sinon. openCloud observe, il ne renouvelle pas : il ne dit
// donc rien d'un renouvellement.
export function certificateNote(t: Translate, certificate: Certificate, reading: CertificateReading, now: number): string {
  if (reading.revoked) {
    return t("cert.note_revoked");
  }
  if (!certificate.chain_valid) {
    return t("cert.note_untrusted");
  }
  if (!certificate.hostname_match) {
    return t("cert.note_wrong_name");
  }
  return t("cert.note_issued", formatDuration(t, now - Date.parse(certificate.not_before)));
}

// Le bloc « Domaines et certificats » d'une machine : un domaine par
// ligne, sa note, et ce qui lui reste. Les sondes sans certificat n'y sont
// pas — elles sont déjà dans le tableau au-dessus.
export function CertificateList({ probes }: { probes: Probe[] }) {
  const t = useT();
  const now = useNow();
  const thresholds = useCertificateThresholds();
  const seen = probes.filter((probe) => probe.certificate !== null);
  return (
    <Card>
      <CardHeader title={t("cert.title")} aside={seen.length === 1 ? t("cert.count_one") : t("cert.count", seen.length)} />
      {seen.length === 0 ? (
        <Empty text={t("cert.machine_empty")} />
      ) : (
        <CardBody gap={12}>
          {seen.map((probe) => {
            // Le filtre au-dessus garantit le certificat ; TypeScript ne le
            // sait pas.
            const certificate = probe.certificate as Certificate;
            const reading = read(certificate, thresholds, now);
            return (
              <div className="kv" key={probe.id}>
                <span className="svc">
                  <Link className="mono" to={`/domaines/${probe.id}`}>
                    {certificate.subject !== "" ? certificate.subject : probe.name}
                  </Link>
                  <span className="secondary">{certificateNote(t, certificate, reading, now)}</span>
                </span>
                <Remaining reading={reading} />
              </div>
            );
          })}
        </CardBody>
      )}
    </Card>
  );
}
