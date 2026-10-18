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

export const api = {
  get: <T>(path: string) => call<T>("GET", path),
  post: <T>(path: string, body?: unknown) => call<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => call<T>("PUT", path, body),
  delete: <T>(path: string) => call<T>("DELETE", path),
};

// La clé à afficher pour une erreur : celle du serveur si c'est un refus,
// sinon le message générique.
export function errorKey(error: unknown): string {
  if (error instanceof ApiError && error.code.includes(".")) {
    return error.code;
  }
  return "error.internal";
}
