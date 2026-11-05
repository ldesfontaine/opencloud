import { useEffect, useMemo, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router";

import { useAPITokens } from "../api/apiTokens";
import { useAuth } from "../api/auth";
import { useCounts } from "../api/counts";
import { useMCPCheck } from "../api/mcpCheck";
import { useLifecycleSettings, useMailSettings } from "../api/settings";
import { useReminderSettings, useThresholdRules } from "../api/thresholds";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { currentSection, sectionOfHash, type SettingsSectionId } from "../lib/settingsSections";
import { AboutSection } from "./settings/AboutSection";
import { AccessSection } from "./settings/AccessSection";
import { AIAgentSection } from "./settings/AIAgentSection";
import { LifecycleSection } from "./settings/LifecycleSection";
import { MailSection } from "./settings/MailSection";
import { ModulesSection } from "./settings/ModulesSection";
import { NotificationsSection } from "./settings/NotificationsSection";
import { SettingsNav } from "./settings/SettingsNav";
import "./Settings.scss";

// Paramètres : sept sections ancrées (l'accès à l'interface, les modules, les
// notifications, l'envoi d'e-mails, la fin de support, l'agent IA et ses jetons
// d'API, la version), toutes dans la page, avec leur navigation. Chaque section lit la
// sienne : l'une qui échoue ne retient pas les autres, et sort de la
// navigation avec elle. Un jeton ne s'affiche qu'une fois, à sa création,
// dans l'état de sa section ; rechargée, elle ne le montre plus.
export function Settings() {
  const t = useT();
  const access = useAuth();
  const thresholds = useThresholdRules();
  const reminder = useReminderSettings();
  const mail = useMailSettings();
  const support = useLifecycleSettings();
  const aiAgent = useMCPCheck();
  const tokens = useAPITokens();
  const counts = useCounts();

  const present: SettingsSectionId[] = [];
  if (access.data !== null) {
    present.push("acces");
  }
  if (counts.data !== null) {
    present.push("modules");
  }
  present.push("notifications");
  if (mail.data !== null) {
    present.push("e-mails");
  }
  if (support.data !== null) {
    present.push("fin-de-support");
  }
  if (aiAgent.data !== null || tokens.data !== null) {
    present.push("agent-ia");
  }
  present.push("a-propos");
  const [current, pick] = useVisibleSection(present);
  // Les sections au-dessus de l'ancre changent la hauteur en arrivant : on attend toutes les lectures.
  const settled = [access, counts, thresholds, reminder, mail, support, aiAgent, tokens].every((read) => read.data !== null || read.error !== null);
  useScrollToHash(settled);

  return (
    <div className="settings-page">
      <PageHead title={t("nav.settings")} subtitle={t("settings.subtitle")} />
      <div className="settings-layout">
        <SettingsNav present={present} current={current} onPick={pick} />
        <div className="settings-main">
          {access.data !== null && <AccessSection state={access.data} />}
          {counts.data !== null && <ModulesSection modules={counts.data.modules} />}
          <NotificationsSection thresholds={thresholds.data} reloadThresholds={thresholds.reload} reminder={reminder.data} reloadReminder={reminder.reload} />
          {mail.data !== null && <MailSection settings={mail.data} reload={mail.reload} />}
          {support.data !== null && <LifecycleSection settings={support.data} reload={support.reload} />}
          {(aiAgent.data !== null || tokens.data !== null) && (
            <AIAgentSection check={aiAgent.data} reloadCheck={aiAgent.reload} tokens={tokens.data} reloadTokens={tokens.reload} />
          )}
          <AboutSection />
        </div>
      </div>
      {/* Les fenêtres des canaux et des silences, à leur adresse. */}
      <Outlet />
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
// aller. Une fois la section prête, la page y va ; une ancre qui change
// ensuite (un lien d'une fenêtre, une ancienne adresse redirigée) y mène aussi.
function useScrollToHash(ready: boolean) {
  const { hash } = useLocation();
  const [scrolledTo, setScrolledTo] = useState<string | null>(null);
  useEffect(() => {
    if (!ready || hash === "" || scrolledTo === hash) {
      return;
    }
    // La section visée peut arriver après la première : on attend qu'elle soit là.
    const target = document.getElementById(sectionOfHash(hash.slice(1)));
    if (target !== null) {
      setScrolledTo(hash);
      target.scrollIntoView();
    }
  });
}
