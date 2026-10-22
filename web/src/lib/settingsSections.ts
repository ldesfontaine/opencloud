// Les sections de Paramètres, dans l'ordre de la page : l'ancre de chacune
// (sans accent, elle se tape dans une adresse) et la clé de son intitulé.
export const settingsSections = [
  { id: "acces", titleKey: "settings.section_access" },
  { id: "alertes", titleKey: "settings.section_alerts" },
  { id: "e-mails", titleKey: "settings.section_mail" },
  { id: "fin-de-support", titleKey: "settings.section_lifecycle" },
  { id: "agent-ia", titleKey: "settings.section_ai" },
] as const;

export type SettingsSectionId = (typeof settingsSections)[number]["id"];

// La section que la navigation met en évidence. crossing : celles qui
// traversent la bande de lecture, juste sous le haut de l'écran. On prend la
// dernière dans l'ordre de la page (la plus avancée qu'on lit déjà) ; en bas
// de page, la dernière section est toujours la bonne, même courte ; sans
// section dans la bande (un espace entre deux), on garde la précédente.
export function currentSection(order: readonly string[], crossing: ReadonlySet<string>, previous: string | null, atBottom: boolean): string | null {
  const last = order[order.length - 1];
  if (last === undefined) {
    return null;
  }
  if (atBottom) {
    return last;
  }
  const inBand = order.filter((id) => crossing.has(id));
  const lastInBand = inBand[inBand.length - 1];
  if (lastInBand !== undefined) {
    return lastInBand;
  }
  return previous !== null && order.includes(previous) ? previous : (order[0] ?? null);
}
