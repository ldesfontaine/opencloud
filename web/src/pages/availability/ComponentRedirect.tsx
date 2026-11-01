import { Navigate, useParams } from "react-router";

import { useStatusComponent } from "../../api/status";
import { Failure } from "../../components/Failure";
import { componentPath } from "../../lib/status";
import { NotFound } from "../NotFound";

// Un composant se retrouve par son seul identifiant (la fiche d'un service
// lie ses composants ainsi) : la fenêtre s'ouvre sur la page de son projet.
export function ComponentRedirect() {
  const { id = "" } = useParams();
  const component = useStatusComponent(id);
  if (component.error?.status === 404) {
    return <NotFound />;
  }
  if (component.data === null) {
    return <Failure error={component.error} />;
  }
  return <Navigate replace to={componentPath(component.data.project_id, component.data.id)} />;
}
