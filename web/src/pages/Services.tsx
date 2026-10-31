import { DirectoryTab } from "../components/DirectoryTab";
import { ProjectsTab } from "../components/ProjectsTab";

// La page Services, sous deux onglets : les services que l'opérateur
// déclare, avec leurs conteneurs dépliés sous chacun (par défaut : c'est lui
// qui porte les liens), et les projets.
export function Services({ tab }: { tab: "annuaire" | "projets" }) {
  if (tab === "projets") {
    return <ProjectsTab />;
  }
  return <DirectoryTab />;
}
