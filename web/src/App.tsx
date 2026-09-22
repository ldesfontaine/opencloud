import { Route, Routes } from "react-router";

import { Layout } from "./components/Layout";
import { AlertPage } from "./pages/Alert";
import { AlertChannelForm } from "./pages/AlertChannelForm";
import { Alerts } from "./pages/Alerts";
import { AlertSilenceNew } from "./pages/AlertSilenceNew";
import { JobPage } from "./pages/Job";
import { JobNew } from "./pages/JobNew";
import { Jobs } from "./pages/Jobs";
import { MachinePage } from "./pages/Machine";
import { MachineNew } from "./pages/MachineNew";
import { MachineToken } from "./pages/MachineToken";
import { Machines } from "./pages/Machines";
import { NotFound } from "./pages/NotFound";
import { ProbePage } from "./pages/Probe";
import { ProbeNew } from "./pages/ProbeNew";
import { Probes } from "./pages/Probes";
import { Overview } from "./pages/Overview";
import { ServicePage } from "./pages/Service";
import { Services } from "./pages/Services";
import { Soon } from "./pages/Soon";
import { StatusAdmin } from "./pages/StatusAdmin";
import { StatusComponentForm } from "./pages/StatusComponentForm";
import { StatusIncidentPage } from "./pages/StatusIncident";
import { StatusIncidentNew } from "./pages/StatusIncidentNew";
import { VisualSystem } from "./pages/VisualSystem";

// Les adresses restent en français, comme l'opérateur les lit ; une route
// par écran, l'API en dessous.
export function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Overview />} />
        <Route path="machines" element={<Machines />} />
        <Route path="machines/nouvelle" element={<MachineNew />} />
        <Route path="machines/jeton" element={<MachineToken />} />
        <Route path="machines/:id" element={<MachinePage />} />
        <Route path="machines/:id/:tab" element={<MachinePage />} />
        <Route path="services" element={<Services />} />
        <Route path="services/:id" element={<ServicePage />} />
        <Route path="domaines" element={<Probes />} />
        <Route path="domaines/nouvelle" element={<ProbeNew />} />
        <Route path="domaines/:id" element={<ProbePage />} />
        <Route path="sauvegardes" element={<Soon navKey="backups" />} />
        <Route path="taches" element={<Jobs />} />
        <Route path="taches/nouvelle" element={<JobNew />} />
        <Route path="taches/:id" element={<JobPage />} />
        <Route path="page-statut" element={<StatusAdmin />} />
        <Route path="page-statut/composants/nouveau" element={<StatusComponentForm />} />
        <Route path="page-statut/composants/:id" element={<StatusComponentForm />} />
        <Route path="page-statut/incidents/nouveau" element={<StatusIncidentNew />} />
        <Route path="page-statut/incidents/:id" element={<StatusIncidentPage />} />
        <Route path="page-statut/:tab" element={<StatusAdmin />} />
        <Route path="alertes" element={<Alerts />} />
        <Route path="alertes/alerte/:id" element={<AlertPage />} />
        <Route path="alertes/canaux/nouveau" element={<AlertChannelForm />} />
        <Route path="alertes/canaux/:id" element={<AlertChannelForm />} />
        <Route path="alertes/silences/nouveau" element={<AlertSilenceNew />} />
        <Route path="alertes/:tab" element={<Alerts />} />
        <Route path="parametres" element={<Soon navKey="settings" />} />
        <Route path="systeme-visuel" element={<VisualSystem />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
