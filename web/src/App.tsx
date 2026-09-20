import { Route, Routes } from "react-router";

import { Layout } from "./components/Layout";
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
        <Route path="alertes" element={<Soon navKey="alerts" />} />
        <Route path="parametres" element={<Soon navKey="settings" />} />
        <Route path="systeme-visuel" element={<VisualSystem />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
