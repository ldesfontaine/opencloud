// Ce que l'API rend, tel quel : des faits, des instants ISO en UTC, des
// durées en secondes ou en millisecondes. Le formatage vit dans le front.

// Le seul endroit qui parle à l'API : JSON, même origine, une erreur
// typée qui porte le code du serveur. Ce code est une clé du catalogue
// quand l'opérateur doit le lire.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    // Le corps de la réponse, quand le refus porte plus qu'une clé (la
    // liste de ce qui lie un service qu'on voulait supprimer).
    readonly payload: unknown = null,
  ) {
    super(code);
  }
}

// Toute écriture part en JSON avec un corps, {} quand elle n'a rien à
// dire : le serveur peut ainsi exiger ce type sans exception.
async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin" };
  // Toute écriture part en JSON, même sans rien à dire : le serveur refuse
  // une écriture qui n'en porte pas, c'est sa garde même-origine.
  if (method !== "GET") {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body ?? {});
  }
  const response = await fetch(path, init);
  if (response.status === 204) {
    return undefined as T;
  }
  if (!response.ok) {
    const payload = await errorPayload(response);
    throw new ApiError(response.status, errorCode(payload), payload);
  }
  return (await response.json()) as T;
}

async function errorPayload(response: Response): Promise<unknown> {
  try {
    return (await response.json()) as unknown;
  } catch {
    return null;
  }
}

function errorCode(payload: unknown): string {
  if (typeof payload === "object" && payload !== null && "error" in payload && typeof payload.error === "string") {
    return payload.error;
  }
  return "internal";
}

// Les identifiants du serveur : hexadécimaux (sondes, tâches, composants),
// UUID (machines), « local », préfixés (svc_, unit_, prj_, apl_), entiers
// (alertes), et « machine:identifiant Docker » pour un conteneur. Ni point,
// ni barre, ni « ? » ni « # » : rien qui change le chemin d'une requête.
const idPattern = /^[A-Za-z0-9_:-]{1,128}$/;

// segment prépare un paramètre de route (ou de requête) à entrer dans un
// chemin d'API. react-router rend « %2F » en « / » et le navigateur résout
// « ../ » avant d'envoyer : un lien forgé ferait partir la requête de la
// page vers une autre ressource. Ce qui n'a pas la forme d'un identifiant
// lève la même erreur qu'une ressource absente.
export function segment(value: string): string {
  if (!idPattern.test(value)) {
    throw new ApiError(404, "not_found");
  }
  return encodeURIComponent(value);
}

export const api = {
  get: <T>(path: string) => call<T>("GET", path),
  post: <T>(path: string, body?: unknown) => call<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => call<T>("PUT", path, body),
  delete: <T>(path: string) => call<T>("DELETE", path),
};

// Ce qui n'est pas un refus du serveur (réseau coupé, levée imprévue) se
// dit comme une erreur interne.
export function failureOf(error: unknown): ApiError {
  return error instanceof ApiError ? error : new ApiError(0, "internal");
}

// La clé à afficher pour une erreur : celle du serveur si c'est un refus,
// sinon le message générique.
export function errorKey(error: unknown): string {
  if (error instanceof ApiError && error.code.includes(".")) {
    return error.code;
  }
  return "error.internal";
}
