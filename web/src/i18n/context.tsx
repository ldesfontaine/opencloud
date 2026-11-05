import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import { fetchCatalog, fetchSession, saveLanguage, type Catalog, type CertificateThresholds, type ResourceName, type ResourceThresholds, type Session, type SessionRole } from "../api/session";
import { productName } from "../lib/product";
import { format } from "./format";

export type Translate = (key: string, ...args: (string | number)[]) => string;

interface I18n {
  version: string;
  commit: string;
  language: string;
  languages: string[];
  // Les seuils d'échéance viennent du serveur avec le reste de la session :
  // le compteur de la vue d'ensemble et la couleur d'une ligne basculent
  // ainsi sur le même chiffre, tenu à un seul endroit.
  certificates: CertificateThresholds;
  thresholds: Record<ResourceName, ResourceThresholds>;
  // Les routes d'écriture qui sont aussi des verbes d'API (puce « MCP »).
  verbRoutes: ReadonlySet<string>;
  // Les rôles d'une machine et les droits qu'ils cochent d'avance.
  roles: SessionRole[];
  t: Translate;
  setLanguage: (code: string) => Promise<void>;
  // Relit la session : après un réglage de seuil, les jauges suivent sans
  // recharger la page.
  reloadSession: () => Promise<void>;
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
    const loaded = await fetchSession();
    const strings = await fetchCatalog(loaded.language);
    document.documentElement.lang = loaded.language;
    setSession(loaded);
    setCatalog(strings);
  }, []);

  useEffect(() => {
    load().catch(() => setUnreachable(true));
  }, [load]);

  const setLanguage = useCallback(
    async (code: string) => {
      await saveLanguage(code);
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
      // Toujours formaté, même sans argument : « %% » doit y devenir « % ».
      return format(template, args);
    };
    return {
      version: session.version,
      commit: session.commit,
      language: session.language,
      languages: session.languages,
      certificates: session.certificate_thresholds,
      thresholds: session.resource_thresholds,
      verbRoutes: new Set(session.verbs.map((verb) => verb.route)),
      roles: session.roles,
      t,
      setLanguage,
      reloadSession: load,
    };
  }, [session, catalog, setLanguage, load]);

  if (unreachable) {
    // Sans catalogue, pas de traduction possible : les deux langues d'un coup.
    const product = productName();
    return (
      <p className="boot-error">
        {product} ne répond pas. · {product} is not answering.
      </p>
    );
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

// Les seuils globaux de chaque ressource, tels que réglés sous Paramètres. Une
// machine qui porte ses propres seuils les dit sur sa fiche (api/thresholds).
export function useThresholds(): Record<ResourceName, ResourceThresholds> {
  return useI18n().thresholds;
}

// Les rôles qu'une machine peut recevoir, avec leur préréglage de droits.
export function useRoles(): SessionRole[] {
  return useI18n().roles;
}

// Dit si une écriture est aussi un verbe d'API que les jetons peuvent porter.
export function useIsVerbRoute(): (route: string) => boolean {
  const { verbRoutes } = useI18n();
  return (route) => verbRoutes.has(route);
}
