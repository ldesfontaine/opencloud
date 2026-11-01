import { useState } from "react";

import { inspectCertificate, type CertificateInspection } from "../api/certificate";
import type { ApiError } from "../api/client";
import { useAction } from "./useAction";

export interface CertificateInspector {
  // Le relevé de l'adresse courante ; null tant qu'aucun n'a été fait pour
  // elle (un relevé d'une autre adresse ne vaut rien pour celle-ci).
  report: CertificateInspection | null;
  busy: boolean;
  error: ApiError | null;
  inspect: () => void;
}

// Le relevé du certificat d'une adresse, tenu par l'assistant : l'étape
// « adresse » le lance (Tester l'accès), l'étape « certificat » le lit, sans
// refaire la connexion tant que l'adresse n'a pas changé.
export function useCertificateInspector(address: string): CertificateInspector {
  const action = useAction();
  const [inspected, setInspected] = useState<{ address: string; report: CertificateInspection } | null>(null);
  const target = address.trim();
  return {
    report: inspected !== null && inspected.address === target ? inspected.report : null,
    busy: action.busy,
    error: action.error,
    inspect: () => action.run(() => inspectCertificate(target), { onDone: (report) => setInspected({ address: target, report }) }),
  };
}
