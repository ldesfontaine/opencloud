import { lazy, Suspense } from "react";
import { Navigate, Route, Routes, useLocation, useParams } from "react-router";

import { Layout } from "./components/Layout";
import { renamedProbePath } from "./lib/probes";
import { AlertPage } from "./pages/Alert";
import { AlertChannelForm } from "./pages/AlertChannelForm";
import { Alerts } from "./pages/Alerts";
import { AlertMuteNew } from "./pages/AlertMuteNew";
import { ApplyDetail } from "./pages/ApplyDetail";
import { Applies } from "./pages/Applies";
import { Audit } from "./pages/Audit";
import { Availability } from "./pages/Availability";
import { PublicPage } from "./pages/availability/PublicPage";
import { SSHKeyMatrix } from "./pages/SSHKeyMatrix";
import { Backups } from "./pages/Backups";
import { ContainerPage } from "./pages/Container";
import { DeclaredServicePage } from "./pages/DeclaredService";
import { FirewallPage } from "./pages/Firewall";
import { FirewallForm } from "./pages/FirewallForm";
import { Firewalls } from "./pages/Firewalls";
import { JobPage } from "./pages/Job";
import { Jobs } from "./pages/Jobs";
import { JobWizard } from "./pages/jobs/JobWizard";
import { MachinePage } from "./pages/Machine";
import { AddMachine } from "./pages/machines/AddMachine";
import { DeclareProxmoxHost } from "./pages/machines/DeclareProxmoxHost";
import { PlacementMachine } from "./pages/machines/PlacementMachine";
import { ReenrollMachine } from "./pages/machines/ReenrollMachine";
import { ProbeWizard } from "./pages/probes/ProbeWizard";
import { ProjectWizard } from "./pages/services/ProjectWizard";
import { ServiceWizard } from "./pages/services/ServiceWizard";
import { Machines } from "./pages/Machines";
import { NotFound } from "./pages/NotFound";
import { ProbePage } from "./pages/Probe";
import { Overview } from "./pages/Overview";
import { Services } from "./pages/Services";
import { Settings } from "./pages/Settings";
import { StatusComponentForm } from "./pages/StatusComponentForm";
import { StatusIncidentPage } from "./pages/StatusIncident";
import { StatusIncidentNew } from "./pages/StatusIncidentNew";

// La fiche d'un conteneur s'ouvrait sur /services/:id avant d'aller sous
// /services/conteneurs/:id : un lien d'avant mène à la nouvelle adresse, avec sa query et son ancre
// (`#journaux`).
function ContainerRedirect() {
  const { id = "" } = useParams();
  const location = useLocation();
  return <Navigate replace to={{ pathname: `/services/conteneurs/${encodeURIComponent(id)}`, search: location.search, hash: location.hash }} />;
}

// La page des sondes s'appelait « Domaines » (/domaines) avant « Disponibilité » :
// un lien d'avant mène à la nouvelle adresse (/domaines/p1/modifier?x devient
// /disponibilite/p1/modifier?x).
function ProbesRedirect() {
  const location = useLocation();
  return <Navigate replace to={{ pathname: renamedProbePath(location.pathname), search: location.search, hash: location.hash }} />;
}

// La page de démonstration du système visuel ne sert qu'en développement :
// Vite remplace import.meta.env.DEV par false au build, et l'import
// dynamique sort la page du bundle de production.
const VisualSystem = import.meta.env.DEV ? lazy(() => import("./pages/VisualSystem").then((page) => ({ default: page.VisualSystem }))) : null;

// Les adresses restent en français, comme l'opérateur les lit ; une route
// par écran, l'API en dessous.
export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Overview />} />
        {/* Les fenêtres modales sont des routes filles : la liste (ou la fiche) reste le fond, et chaque fenêtre garde son adresse. */}
        <Route path="machines" element={<Machines />}>
          <Route path="nouvelle" element={<AddMachine />} />
        </Route>
        <Route path="machines/:id" element={<MachinePage />}>
          <Route path="reenroller" element={<ReenrollMachine />} />
          <Route path="proxmox" element={<DeclareProxmoxHost />} />
          <Route path="emplacement" element={<PlacementMachine />} />
        </Route>
        <Route path="machines/:id/:tab" element={<MachinePage />} />
        <Route path="services" element={<Services tab="annuaire" />}>
          <Route path="annuaire/nouveau" element={<ServiceWizard />} />
        </Route>
        {/* Les anciennes adresses des onglets Conteneurs et Annuaire : les liens enregistrés suivent. */}
        <Route path="services/conteneurs" element={<Navigate to="/services" replace />} />
        <Route path="services/annuaire" element={<Navigate to="/services" replace />} />
        {/* Le journal des fichiers appliqués : des segments fixes, avant l'identifiant d'un service. */}
        <Route path="services/annuaire/journal" element={<Applies />} />
        <Route path="services/annuaire/journal/:id" element={<ApplyDetail />} />
        <Route path="services/annuaire/:id" element={<DeclaredServicePage />}>
          <Route path="modifier" element={<ServiceWizard />} />
        </Route>
        <Route path="services/projets" element={<Services tab="projets" />}>
          <Route path="nouveau" element={<ProjectWizard />} />
          <Route path=":id" element={<ProjectWizard />} />
        </Route>
        <Route path="services/conteneurs/:id" element={<ContainerPage />} />
        {/* L'ancienne adresse d'une fiche de conteneur, gardée pour les liens déjà partagés. */}
        <Route path="services/:id" element={<ContainerRedirect />} />
        <Route path="disponibilite" element={<Availability tab="probes" />}>
          <Route path="nouvelle" element={<ProbeWizard />} />
        </Route>
        <Route path="disponibilite/pages" element={<Availability tab="pages" />} />
        <Route path="disponibilite/pages/:id" element={<PublicPage />} />
        <Route path="disponibilite/composants/nouveau" element={<StatusComponentForm />} />
        <Route path="disponibilite/composants/:id" element={<StatusComponentForm />} />
        <Route path="disponibilite/incidents" element={<Availability tab="incidents" />} />
        <Route path="disponibilite/incidents/nouveau" element={<StatusIncidentNew />} />
        <Route path="disponibilite/incidents/:id" element={<StatusIncidentPage />} />
        <Route path="disponibilite/:id" element={<ProbePage />}>
          <Route path="modifier" element={<ProbeWizard />} />
        </Route>
        {/* L'ancien nom de la page : les liens enregistrés suivent, chemin, query et ancre gardés. */}
        <Route path="domaines/*" element={<ProbesRedirect />} />
        <Route path="sauvegardes" element={<Backups />} />
        <Route path="pare-feu" element={<Firewalls />} />
        <Route path="pare-feu/nouveau" element={<FirewallForm />} />
        <Route path="pare-feu/:id" element={<FirewallPage />} />
        <Route path="pare-feu/:id/modifier" element={<FirewallForm />} />
        <Route path="taches" element={<Jobs />}>
          <Route path="nouvelle" element={<JobWizard />} />
        </Route>
        <Route path="taches/:id" element={<JobPage />}>
          <Route path="modifier" element={<JobWizard />} />
        </Route>
        <Route path="page-statut/*" element={<Navigate replace to="/disponibilite/pages" />} />
        <Route path="alertes" element={<Alerts />} />
        <Route path="alertes/alerte/:id" element={<AlertPage />} />
        <Route path="alertes/canaux/nouveau" element={<AlertChannelForm />} />
        <Route path="alertes/canaux/:id" element={<AlertChannelForm />} />
        <Route path="alertes/silences/nouveau" element={<AlertMuteNew />} />
        <Route path="alertes/:tab" element={<Alerts />} />
        <Route path="parametres" element={<Settings />} />
        <Route path="cles-ssh" element={<SSHKeyMatrix />} />
        <Route path="journal" element={<Audit />} />
        {VisualSystem && (
          <Route
            path="systeme-visuel"
            element={
              <Suspense fallback={null}>
                <VisualSystem />
              </Suspense>
            }
          />
        )}
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
