const csrfCookieName = "regstair_csrf";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly classification: string,
  ) {
    super(classification);
    this.name = "ApiError";
  }
}

export class SessionExpiredError extends ApiError {
  constructor() {
    super(401, "session_expired");
    this.name = "SessionExpiredError";
  }
}

function cookieValue(name: string): string {
  const prefix = `${name}=`;
  const value = document.cookie
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix))
    ?.slice(prefix.length);

  return value ? decodeURIComponent(value) : "";
}

function isMutation(method: string): boolean {
  return !["GET", "HEAD", "OPTIONS"].includes(method);
}

async function errorClassification(response: Response): Promise<string> {
  if (response.headers.get("Content-Type")?.includes("application/json")) {
    const payload = (await response.json()) as { error?: unknown | { code?: string } };
    if (typeof payload.error === "string" && payload.error.length > 0) return payload.error;
    if (payload.error && typeof payload.error === "object") {
      const structured = payload.error as { code?: unknown };
      if (typeof structured.code === "string") return structured.code;
    }
  }
  return `http_${response.status}`;
}

export async function apiRequest<T = void>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (isMutation(method)) headers.set("X-CSRF-Token", cookieValue(csrfCookieName));

  const response = await fetch(path, {
    ...init,
    method,
    headers,
    credentials: "same-origin",
  });

  if (response.status === 401) throw new SessionExpiredError();
  if (!response.ok) throw new ApiError(response.status, await errorClassification(response));
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}
