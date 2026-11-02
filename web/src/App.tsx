import { lazy, Suspense } from "react";
import { Navigate, Route, Routes, useLocation, useParams } from "react-router";

import { Layout } from "./components/Layout";
import { renamedAlertLocation } from "./lib/alerts";
import { renamedProbePath } from "./lib/probes";
import { AlertPage } from "./pages/Alert";
import { Alerts } from "./pages/Alerts";
import { ApplyDetail } from "./pages/ApplyDetail";
import { Applies } from "./pages/Applies";
import { Audit } from "./pages/Audit";
import { Availability } from "./pages/Availability";
import { ComponentModal } from "./pages/availability/ComponentModal";
import { ComponentRedirect } from "./pages/availability/ComponentRedirect";
import { IncidentEditModal } from "./pages/availability/IncidentEditModal";
import { IncidentModal } from "./pages/availability/IncidentModal";
import { PublicPage } from "./pages/availability/PublicPage";
import { Backups } from "./pages/Backups";
import { DestinationModal } from "./pages/backups/DestinationModal";
import { KeyringModal } from "./pages/backups/KeyringModal";
import { PlanWizard } from "./pages/backups/PlanWizard";
import { ContainerPage } from "./pages/Container";
import { DeclaredServicePage } from "./pages/DeclaredService";
import { FirewallPage } from "./pages/Firewall";
import { FirewallWizard } from "./pages/firewalls/FirewallWizard";
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
import { LinkModal } from "./pages/services/LinkModal";
import { Machines } from "./pages/Machines";
import { NotFound } from "./pages/NotFound";
import { ProbePage } from "./pages/Probe";
import { Overview } from "./pages/Overview";
import { Services } from "./pages/Services";
import { Settings } from "./pages/Settings";
import { ChannelWizard } from "./pages/settings/ChannelWizard";
import { MuteWizard } from "./pages/settings/MuteWizard";
import { StatusIncidentPage } from "./pages/StatusIncident";

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

// Les canaux et les silences étaient des onglets de /alertes (et leurs
// formulaires des pages) avant Paramètres › Notifications : un lien d'avant
// mène à la fenêtre ou à la section qui les porte, avec sa query et son ancre.
// Les autres onglets (acquittées, résolues) mènent à la boîte.
function AlertsRedirect() {
  const location = useLocation();
  const target = renamedAlertLocation(location.pathname);
  return <Navigate replace to={{ pathname: target.pathname, search: location.search, hash: target.hash !== "" ? target.hash : location.hash }} />;
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
        <Route path="machines" element={<Machines tab="list" />}>
          <Route path="nouvelle" element={<AddMachine />} />
        </Route>
        <Route path="machines/acces-ssh" element={<Machines tab="ssh" />} />
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
          <Route path="liens/nouveau" element={<LinkModal />} />
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
        <Route path="disponibilite/pages/:id" element={<PublicPage />}>
          <Route path="composants/nouveau" element={<ComponentModal />} />
          <Route path="composants/:componentId" element={<ComponentModal />} />
        </Route>
        <Route path="disponibilite/composants/:id" element={<ComponentRedirect />} />
        <Route path="disponibilite/incidents" element={<Availability tab="incidents" />}>
          <Route path="nouveau" element={<IncidentModal />} />
        </Route>
        <Route path="disponibilite/incidents/:id" element={<StatusIncidentPage />}>
          <Route path="modifier" element={<IncidentEditModal />} />
        </Route>
        <Route path="disponibilite/:id" element={<ProbePage />}>
          <Route path="modifier" element={<ProbeWizard />} />
        </Route>
        {/* L'ancien nom de la page : les liens enregistrés suivent, chemin, query et ancre gardés. */}
        <Route path="domaines/*" element={<ProbesRedirect />} />
        <Route path="sauvegardes" element={<Backups />}>
          <Route path="cle" element={<KeyringModal />} />
          <Route path="destinations/nouvelle" element={<DestinationModal />} />
          <Route path="plans/nouveau" element={<PlanWizard />} />
          <Route path="plans/:subject/modifier" element={<PlanWizard />} />
        </Route>
        <Route path="pare-feu" element={<Firewalls />}>
          <Route path="nouveau" element={<FirewallWizard />} />
        </Route>
        <Route path="pare-feu/:id" element={<FirewallPage />}>
          <Route path="flux" element={null} />
          <Route path="modifier" element={<FirewallWizard />} />
        </Route>
        <Route path="taches" element={<Jobs />}>
          <Route path="nouvelle" element={<JobWizard />} />
        </Route>
        <Route path="taches/:id" element={<JobPage />}>
          <Route path="modifier" element={<JobWizard />} />
        </Route>
        <Route path="page-statut/*" element={<Navigate replace to="/disponibilite/pages" />} />
        <Route path="alertes" element={<Alerts />} />
        <Route path="alertes/alerte/:id" element={<AlertPage />} />
        {/* Les onglets et les formulaires d'avant : les liens enregistrés suivent. */}
        <Route path="alertes/*" element={<AlertsRedirect />} />
        <Route path="parametres" element={<Settings />}>
          <Route path="canaux/nouveau" element={<ChannelWizard />} />
          <Route path="canaux/:id" element={<ChannelWizard />} />
          <Route path="silences/nouveau" element={<MuteWizard />} />
        </Route>
        {/* La vue globale des clés SSH s'appelait « Clés SSH » (/cles-ssh) avant d'être un onglet de Machines. */}
        <Route path="cles-ssh" element={<Navigate to="/machines/acces-ssh" replace />} />
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
