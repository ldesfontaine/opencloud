import { lazy, Suspense } from "react";
import { Navigate, Route, Routes, useLocation, useParams } from "react-router";

import { Layout } from "./components/Layout";
import { AlertPage } from "./pages/Alert";
import { AlertChannelForm } from "./pages/AlertChannelForm";
import { Alerts } from "./pages/Alerts";
import { AlertMuteNew } from "./pages/AlertMuteNew";
import { ApplyDetail } from "./pages/ApplyDetail";
import { Applies } from "./pages/Applies";
import { Audit } from "./pages/Audit";
import { Backups } from "./pages/Backups";
import { ContainerPage } from "./pages/Container";
import { DeclaredServicePage } from "./pages/DeclaredService";
import { DeclaredServiceForm } from "./pages/DeclaredServiceForm";
import { JobPage } from "./pages/Job";
import { JobForm } from "./pages/JobForm";
import { Jobs } from "./pages/Jobs";
import { MachinePage } from "./pages/Machine";
import { MachineNew } from "./pages/MachineNew";
import { MachineRights } from "./pages/MachineRights";
import { MachineToken } from "./pages/MachineToken";
import { Machines } from "./pages/Machines";
import { NotFound } from "./pages/NotFound";
import { OAuthConsent } from "./pages/OAuthConsent";
import { ProbePage } from "./pages/Probe";
import { ProbeForm } from "./pages/ProbeForm";
import { Probes } from "./pages/Probes";
import { ProjectForm } from "./pages/ProjectForm";
import { Overview } from "./pages/Overview";
import { Services } from "./pages/Services";
import { Settings } from "./pages/Settings";
import { StatusAdmin } from "./pages/StatusAdmin";
import { StatusComponentForm } from "./pages/StatusComponentForm";
import { StatusIncidentPage } from "./pages/StatusIncident";
import { StatusIncidentNew } from "./pages/StatusIncidentNew";
import { StatusPageForm } from "./pages/StatusPageForm";

// La fiche d'un conteneur s'ouvrait sur /services/:id avant d'aller sous
// /services/conteneurs/:id : un lien d'avant mène à la nouvelle adresse, avec sa query et son ancre
// (`#journaux`).
function ContainerRedirect() {
  const { id = "" } = useParams();
  const location = useLocation();
  return <Navigate replace to={{ pathname: `/services/conteneurs/${encodeURIComponent(id)}`, search: location.search, hash: location.hash }} />;
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
      {/* Le consentement d'un agent IA, où /oauth/authorize envoie : hors de l'interface. */}
      <Route path="oauth/consentir" element={<OAuthConsent />} />
      <Route element={<Layout />}>
        <Route index element={<Overview />} />
        <Route path="machines" element={<Machines />} />
        <Route path="machines/nouvelle" element={<MachineNew />} />
        <Route path="machines/jeton" element={<MachineToken />} />
        <Route path="machines/:id" element={<MachinePage />} />
        <Route path="machines/:id/droits" element={<MachineRights />} />
        <Route path="machines/:id/:tab" element={<MachinePage />} />
        <Route path="services" element={<Services tab="annuaire" />} />
        <Route path="services/conteneurs" element={<Services tab="conteneurs" />} />
        {/* L'ancienne adresse de l'onglet : les liens enregistrés suivent. */}
        <Route path="services/annuaire" element={<Navigate to="/services" replace />} />
        <Route path="services/annuaire/nouveau" element={<DeclaredServiceForm />} />
        {/* Le journal des fichiers appliqués : des segments fixes, avant l'identifiant d'un service. */}
        <Route path="services/annuaire/journal" element={<Applies />} />
        <Route path="services/annuaire/journal/:id" element={<ApplyDetail />} />
        <Route path="services/annuaire/:id/modifier" element={<DeclaredServiceForm />} />
        <Route path="services/annuaire/:id" element={<DeclaredServicePage />} />
        <Route path="services/projets" element={<Services tab="projets" />} />
        <Route path="services/projets/nouveau" element={<ProjectForm />} />
        <Route path="services/projets/:id" element={<ProjectForm />} />
        <Route path="services/conteneurs/:id" element={<ContainerPage />} />
        {/* L'ancienne adresse d'une fiche de conteneur, gardée pour les liens déjà partagés. */}
        <Route path="services/:id" element={<ContainerRedirect />} />
        <Route path="domaines" element={<Probes />} />
        <Route path="domaines/nouvelle" element={<ProbeForm />} />
        <Route path="domaines/:id/modifier" element={<ProbeForm />} />
        <Route path="domaines/:id" element={<ProbePage />} />
        <Route path="sauvegardes" element={<Backups />} />
        <Route path="taches" element={<Jobs />} />
        <Route path="taches/nouvelle" element={<JobForm />} />
        <Route path="taches/:id/modifier" element={<JobForm />} />
        <Route path="taches/:id" element={<JobPage />} />
        <Route path="page-statut" element={<StatusAdmin />} />
        <Route path="page-statut/pages/nouvelle" element={<StatusPageForm />} />
        <Route path="page-statut/pages/:id" element={<StatusPageForm />} />
        <Route path="page-statut/composants/nouveau" element={<StatusComponentForm />} />
        <Route path="page-statut/composants/:id" element={<StatusComponentForm />} />
        <Route path="page-statut/incidents/nouveau" element={<StatusIncidentNew />} />
        <Route path="page-statut/incidents/:id" element={<StatusIncidentPage />} />
        <Route path="page-statut/:tab" element={<StatusAdmin />} />
        <Route path="alertes" element={<Alerts />} />
        <Route path="alertes/alerte/:id" element={<AlertPage />} />
        <Route path="alertes/canaux/nouveau" element={<AlertChannelForm />} />
        <Route path="alertes/canaux/:id" element={<AlertChannelForm />} />
        <Route path="alertes/silences/nouveau" element={<AlertMuteNew />} />
        <Route path="alertes/:tab" element={<Alerts />} />
        <Route path="parametres" element={<Settings />} />
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
