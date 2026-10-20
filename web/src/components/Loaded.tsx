import type { ReactNode } from "react";

import type { Resource } from "../hooks/useResource";
import { NotFound } from "../pages/NotFound";
import { Failure } from "./Failure";

// L'état d'une ressource lue, avant de la montrer : absente (404), en
// échec, pas encore arrivée, ou là. Une relecture qui échoue garde les
// données à l'écran : seule la première lecture ratée se dit.
export function Loaded<T>({ resource, notFound, children }: { resource: Resource<T>; notFound?: ReactNode; children: (data: T) => ReactNode }) {
  if (resource.error?.status === 404) {
    return notFound ?? <NotFound />;
  }
  if (resource.data === null) {
    return <Failure error={resource.error} />;
  }
  return children(resource.data);
}
