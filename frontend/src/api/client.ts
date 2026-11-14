export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

type Params = Record<string, string | number | boolean | undefined | null>

const BASE = import.meta.env.VITE_API_BASE ?? '/api'

export async function get<T>(path: string, params: Params = {}, signal?: AbortSignal): Promise<T> {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== '') qs.set(k, String(v))
  }
  const url = `${BASE}${path}${qs.size ? `?${qs}` : ''}`
  const res = await fetch(url, { signal, headers: { Accept: 'application/json' } })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const body = await res.json()
      msg = body.error ?? body.message ?? msg
    } catch {
      /* body was not JSON */
    }
    throw new ApiError(res.status, msg)
  }
  return (await res.json()) as T
}

/** POST / DELETE with a JSON body; same error handling as get(). */
export async function send<T>(method: 'POST' | 'PUT' | 'DELETE', path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const b = await res.json()
      msg = b.error ?? b.message ?? msg
    } catch {
      /* body was not JSON */
    }
    throw new ApiError(res.status, msg)
  }
  return (await res.json()) as T
}
