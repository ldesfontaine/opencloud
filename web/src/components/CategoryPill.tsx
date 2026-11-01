import type { Category, DeclaredService } from "../api/directory";
import { useT } from "../i18n/context";
import { Pill } from "./Pill";

// Ce que le service est, en pastille : « Serveur web / proxy », « SSO »… La
// correction de l'opérateur l'emporte sur ce que l'image dit ; « Application »
// n'arrive que si l'image est inconnue. Un type connu se distingue d'une
// application au premier coup d'œil.
export function shownCategory(service: Pick<DeclaredService, "category" | "category_deduced">): Category {
  return service.category === "" ? service.category_deduced : service.category;
}

export function CategoryPill({ service }: { service: Pick<DeclaredService, "category" | "category_deduced"> }) {
  const t = useT();
  const category = shownCategory(service);
  return (
    <Pill tone={category === "application" ? "neutral" : "accent"} dot={false}>
      {t(`directory.category_${category}`)}
    </Pill>
  );
}
