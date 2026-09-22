import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { api } from "../api/client";
import type { CertificateThresholds, DiskThresholds, Session } from "../api/types";
import { format } from "./format";

export type Translate = (key: string, ...args: (string | number)[]) => string;

type Catalog = Record<string, string>;

interface I18n {
  version: string;
  language: string;
  languages: string[];
  // Les seuils d'échéance viennent du serveur avec le reste de la session :
  // le compteur de la vue d'ensemble et la couleur d'une ligne basculent
  // ainsi sur le même chiffre, tenu à un seul endroit.
  certificates: CertificateThresholds;
  disks: DiskThresholds;
  t: Translate;
  setLanguage: (code: string) => Promise<void>;
}

const I18nContext = createContext<I18n | null>(null);

// Charge la session puis le catalogue de sa langue ; rien ne s'affiche
// avant, pour ne jamais montrer une clé nue. Changer de langue recharge le
// catalogue sans recharger la page.
export function I18nProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null);
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [unreachable, setUnreachable] = useState(false);

  const load = useCallback(async () => {
    const loaded = await api.get<Session>("/api/session");
    const strings = await api.get<Catalog>(`/api/i18n/${loaded.language}`);
    document.documentElement.lang = loaded.language;
    setSession(loaded);
    setCatalog(strings);
  }, []);

  useEffect(() => {
    load().catch(() => setUnreachable(true));
  }, [load]);

  const setLanguage = useCallback(
    async (code: string) => {
      await api.put("/api/session/language", { language: code });
      await load();
    },
    [load],
  );

  const value = useMemo<I18n | null>(() => {
    if (!session || !catalog) {
      return null;
    }
    const t: Translate = (key, ...args) => {
      const template = catalog[key];
      if (template === undefined) {
        return `[${key}]`;
      }
      return args.length > 0 ? format(template, args) : template;
    };
    return {
      version: session.version,
      language: session.language,
      languages: session.languages,
      certificates: session.certificate_thresholds,
      disks: session.disk_thresholds,
      t,
      setLanguage,
    };
  }, [session, catalog, setLanguage]);

  if (unreachable) {
    // Sans catalogue, pas de traduction possible : les deux langues d'un coup.
    return <p className="boot-error">openCloud ne répond pas. · openCloud is not answering.</p>;
  }
  if (!value) {
    return null;
  }
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18n {
  const value = useContext(I18nContext);
  if (!value) {
    throw new Error("useI18n outside I18nProvider");
  }
  return value;
}

export function useT(): Translate {
  return useI18n().t;
}

// Les seuils d'échéance d'un certificat, tels que le serveur les tient.
export function useCertificateThresholds(): CertificateThresholds {
  return useI18n().certificates;
}

export function useDiskThresholds(): DiskThresholds {
  return useI18n().disks;
}
