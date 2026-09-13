// Le seul endroit qui parle à l'API : JSON, même origine, une erreur
// typée qui porte le code du serveur. Ce code est une clé du catalogue
// quand l'opérateur doit le lire.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
  ) {
    super(code);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin" };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const response = await fetch(path, init);
  if (response.status === 204) {
    return undefined as T;
  }
  if (!response.ok) {
    throw new ApiError(response.status, await errorCode(response));
  }
  return (await response.json()) as T;
}

async function errorCode(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as { error?: string };
    return payload.error ?? "internal";
  } catch {
    return "internal";
  }
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
