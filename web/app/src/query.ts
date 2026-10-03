import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { ApiError } from './api'

/** How often lists with live state (online devices, running tunnels) refresh. */
export const POLL_MS = 30_000

/**
 * A query client that reacts to session-level errors from any request:
 * 401 → the session is gone, re-run bootstrap (shows the login screen);
 * 403 totp_setup_required → TOTP became mandatory, reload /me (shows only the security page).
 */
export function createQueryClient() {
  const onError = (err: unknown) => {
    if (!(err instanceof ApiError)) return
    if (err.status === 401 && err.code === 'unauthorized') {
      void client.invalidateQueries({ queryKey: ['bootstrap'] })
    } else if (err.code === 'totp_setup_required') {
      void client.invalidateQueries({ queryKey: ['me'] })
    }
  }
  const client: QueryClient = new QueryClient({
    queryCache: new QueryCache({ onError }),
    mutationCache: new MutationCache({ onError }),
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false, staleTime: 10_000 },
      mutations: { retry: false },
    },
  })
  return client
}
