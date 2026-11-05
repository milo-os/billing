/**
 * Every call goes through staff-portal's same-origin proxy at
 * `/api/internal/…`, which forwards to Milo with the viewing staff member's
 * own session token. No plugin-owned backend and no new credential: the page
 * can read exactly what that staff member can.
 *
 * staff-portal wraps each buffered response as `{ requestId, code, data,
 * path }`; the upstream object is at `.data`.
 *
 * `@tanstack/react-query` and `react-router` are host-shared singletons (see
 * vite.config.ts): never create a QueryClient here, and read route params
 * with `useParams()`.
 */
export const PLUGIN_ID = 'billing.staff-portal.miloapis.com';

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

interface ProxyEnvelope<T> {
  data: T;
}

async function unwrap<T>(res: Response, path: string): Promise<T> {
  if (!res.ok) {
    throw new ApiError(res.status, `Request failed (${res.status}): ${path}`);
  }
  return ((await res.json()) as ProxyEnvelope<T>).data;
}

export async function proxyGet<T>(path: string): Promise<T> {
  const res = await fetch(`/api/internal${path}`, { headers: { Accept: 'application/json' } });
  return unwrap<T>(res, path);
}

export async function proxyPost<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`/api/internal${path}`, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return unwrap<T>(res, path);
}

interface RawList<T> {
  items?: T[];
  metadata?: { continue?: string };
}

export interface ListResult<T> {
  items: T[];
  /** True when the walk stopped at `maxPages` with more left to read. */
  truncated: boolean;
}

const PAGE_SIZE = 500;

/**
 * Lists a collection, following `continue` tokens. Fleet lists (every
 * invoice on the platform) can be long, so the walk stops at `maxPages` and
 * says so instead of loading without end.
 */
export async function listAll<T>(path: string, maxPages = 40): Promise<ListResult<T>> {
  const items: T[] = [];
  let token: string | undefined;
  for (let page = 0; page < maxPages; page++) {
    const sep = path.includes('?') ? '&' : '?';
    const query = `limit=${PAGE_SIZE}${token ? `&continue=${encodeURIComponent(token)}` : ''}`;
    const body = await proxyGet<RawList<T>>(`${path}${sep}${query}`);
    items.push(...(body.items ?? []));
    token = body.metadata?.continue;
    if (!token) return { items, truncated: false };
  }
  return { items, truncated: true };
}

/**
 * Runs `fn` over `items` with at most `limit` in flight. staff-portal serves
 * HTTP/2, so this cap is the only thing keeping a fleet fan-out polite.
 */
export async function mapWithConcurrency<T, R>(
  items: readonly T[],
  limit: number,
  fn: (item: T, index: number) => Promise<R>
): Promise<R[]> {
  const results = new Array<R>(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const index = next++;
      results[index] = await fn(items[index], index);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}
