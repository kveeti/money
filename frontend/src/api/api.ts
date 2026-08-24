export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const isForm = init?.body instanceof FormData;
  const res = await fetch(path, {
    credentials: "include",
    ...init,
    headers: {
      ...(isForm ? {} : { "content-type": "application/json" }),
      ...init?.headers,
    },
  });

  const text = await res.text();

  let body: unknown;
  try {
    body = text ? JSON.parse(text) : undefined;
  } catch {
    throw new ApiError(res.status, text || res.statusText);
  }

  if (res.status === 401 && window.location.pathname !== "/login") {
    window.location.assign("/login");
  }

  if (!res.ok) {
    const error = (body as { error?: string })?.error;
    throw new ApiError(res.status, error || text || res.statusText);
  }

  return body as T;
}

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}
