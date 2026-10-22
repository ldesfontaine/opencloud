import { Link } from "react-router";

import type { Certificate, Probe } from "../api/probes";
import { useNow } from "../hooks/useNow";
import { useCertificateThresholds, useT } from "../i18n/context";
import { certificateNote, read, type CertificateReading } from "../lib/certificates";
import { probePath } from "../lib/probes";
import { Card, CardBody, CardHeader, Empty } from "./Card";
import { Pill } from "./Pill";

// Ce qui reste, en jours. Un certificat sain reste discret, en texte ; dès
// qu'il approche de sa fin, l'échéance se lit en pastille, au ton du seuil.
// Un certificat expiré dit depuis combien de temps plutôt qu'un nombre
// négatif. `plain` : la pastille d'état est juste à côté et dit déjà le ton.
export function Remaining({ reading, plain = false }: { reading: CertificateReading; plain?: boolean }) {
  const t = useT();
  const text = reading.days < 0 ? t("cert.expired_days", -reading.days) : t("cert.days", reading.days);
  if (plain || reading.tone === "ok") {
    return <span className="mono nowrap secondary">{text}</span>;
  }
  return (
    <Pill tone={reading.tone} small>
      <span className="mono">{text}</span>
    </Pill>
  );
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

// Le bloc « Certificats » de l'onglet Disponibilité d'une machine : une cible par
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
                  <Link className="mono" to={probePath(probe.id)}>
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
