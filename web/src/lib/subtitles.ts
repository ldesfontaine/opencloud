import type { Translate } from "../i18n/context";

// Le sous-titre des pages qui comptent les machines : rien, une, plusieurs.
export function machinesSubtitle(t: Translate, total: number, online: number): string {
  if (total === 0) {
    return t("overview.subtitle");
  }
  if (total === 1) {
    return t("machines.subtitle_one", online);
  }
  return t("machines.subtitle", total, online);
}

// Le sous-titre de la page des tâches : rien, ou le compte et ce qui
// demande attention.
export function jobsSubtitle(t: Translate, total: number, attention: number): string {
  if (total === 0) {
    return t("jobs.subtitle_none");
  }
  if (total === 1) {
    return t(attention === 0 ? "jobs.subtitle_one" : "jobs.subtitle_one_attention");
  }
  if (attention === 0) {
    return t("jobs.subtitle_ok", total);
  }
  return t("jobs.subtitle_attention", total, attention);
}
