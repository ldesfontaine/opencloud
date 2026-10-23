import { useEffect, useMemo, useRef, useState } from "react";

import { useAPITokens } from "../api/apiTokens";
import { useAuth } from "../api/auth";
import { useMCPCheck } from "../api/mcpCheck";
import { useDiskSettings, useLifecycleSettings, useMailSettings } from "../api/settings";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { currentSection, type SettingsSectionId } from "../lib/settingsSections";
import { AccessSection } from "./settings/AccessSection";
import { AIAgentSection } from "./settings/AIAgentSection";
import { DiskSection } from "./settings/DiskSection";
import { LifecycleSection } from "./settings/LifecycleSection";
import { MailSection } from "./settings/MailSection";
import { SettingsNav } from "./settings/SettingsNav";
import "./Settings.scss";

// Paramètres : cinq sections ancrées (l'accès à l'interface, les alertes du
// disque, l'envoi d'e-mails, la fin de support, l'agent IA et ses jetons
// d'API), toutes dans la page, avec leur navigation. Chaque section lit la
// sienne : l'une qui échoue ne retient pas les autres, et sort de la
// navigation avec elle. Un jeton ne s'affiche qu'une fois, à sa création,
// dans l'état de sa section ; rechargée, elle ne le montre plus.
export function Settings() {
  const t = useT();
  const access = useAuth();
  const disks = useDiskSettings();
  const mail = useMailSettings();
  const support = useLifecycleSettings();
  const aiAgent = useMCPCheck();
  const tokens = useAPITokens();

  const present: SettingsSectionId[] = [];
  if (access.data !== null) {
    present.push("acces");
  }
  if (disks.data !== null) {
    present.push("alertes");
  }
  if (mail.data !== null) {
    present.push("e-mails");
  }
  if (support.data !== null) {
    present.push("fin-de-support");
  }
  if (aiAgent.data !== null || tokens.data !== null) {
    present.push("agent-ia");
  }
  const [current, pick] = useVisibleSection(present);
  useScrollToHash(present.length > 0);

  return (
    <div className="settings-page">
      <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />
      <div className="settings-layout">
        <SettingsNav present={present} current={current} onPick={pick} />
        <div className="settings-main">
          {access.data !== null && <AccessSection state={access.data} />}
          {disks.data !== null && <DiskSection settings={disks.data} reload={disks.reload} />}
          {mail.data !== null && <MailSection settings={mail.data} reload={mail.reload} />}
          {support.data !== null && <LifecycleSection settings={support.data} reload={support.reload} />}
          {(aiAgent.data !== null || tokens.data !== null) && (
            <AIAgentSection check={aiAgent.data} reloadCheck={aiAgent.reload} tokens={tokens.data} reloadTokens={tokens.reload} />
          )}
        </div>
      </div>
    </div>
  );
}

// La section que la page montre : celles qui traversent une bande sous le
// haut de l'écran, jugées par currentSection. Un observateur par liste de
// sections présentes ; il se défait avec elle.
function useVisibleSection(present: readonly SettingsSectionId[]): [string | null, (id: string) => void] {
  const [current, setCurrent] = useState<string | null>(null);
  // Un lien cliqué reste la section en évidence jusqu'à ce que l'opérateur
  // défile lui-même : une section courte au bas de la page ne peut pas monter
  // sous la bande, et la dernière passerait devant celle qu'il a choisie.
  const pinned = useRef<string | null>(null);
  const key = present.join(" ");
  const order = useMemo(() => (key === "" ? [] : key.split(" ")), [key]);

  useEffect(() => {
    if (order.length === 0 || typeof IntersectionObserver === "undefined") {
      return;
    }
    const crossing = new Set<string>();
    const update = () => {
      if (pinned.current !== null) {
        setCurrent(pinned.current);
        return;
      }
      const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2;
      setCurrent((previous) => currentSection(order, crossing, previous, atBottom));
    };
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            crossing.add(entry.target.id);
          } else {
            crossing.delete(entry.target.id);
          }
        }
        update();
      },
      // La bande : de 120 px sous le haut (l'en-tête et la rangée d'onglets du
      // téléphone) jusqu'aux 40 % de l'écran.
      { rootMargin: "-120px 0px -60% 0px" },
    );
    for (const id of order) {
      const section = document.getElementById(id);
      if (section !== null) {
        observer.observe(section);
      }
    }
    // Le bas de la page ne fait traverser aucune bande de plus : on l'écoute.
    window.addEventListener("scroll", update, { passive: true });
    const unpin = () => {
      pinned.current = null;
      update();
    };
    const byHand = ["wheel", "touchmove", "keydown"] as const;
    for (const event of byHand) {
      window.addEventListener(event, unpin, { passive: true });
    }
    update();
    return () => {
      observer.disconnect();
      window.removeEventListener("scroll", update);
      for (const event of byHand) {
        window.removeEventListener(event, unpin);
      }
    };
  }, [order]);

  const pick = (id: string) => {
    pinned.current = id;
    setCurrent(id);
  };
  return [current, pick];
}

// Une adresse avec son ancre (/parametres#e-mails) arrive avant les
// sections, qui se lisent après coup : le navigateur n'a rien trouvé où
// aller. Une fois la première prête, la page y va.
function useScrollToHash(ready: boolean) {
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!ready || done) {
      return;
    }
    const id = window.location.hash.slice(1);
    if (id === "") {
      setDone(true);
      return;
    }
    // La section visée peut arriver après la première : on attend qu'elle soit là.
    const target = document.getElementById(id);
    if (target !== null) {
      setDone(true);
      target.scrollIntoView();
    }
  });
}
