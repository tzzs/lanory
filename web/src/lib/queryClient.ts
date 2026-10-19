import { QueryClient } from '@tanstack/react-query'

/**
 * Shared QueryClient.
 *
 * Retries are limited and stale times are generous: Lanory talks to a router,
 * not a datacentre, and the UI already receives live updates over SSE.
 */
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 10_000,
      gcTime: 5 * 60_000,
      retry: (failureCount, error) => {
        // Do not hammer the router when it is unreachable.
        if (error instanceof Error && error.message.includes('network unreachable')) return false
        return failureCount < 2
      },
      retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 10_000),
      refetchOnWindowFocus: false,
    },
    mutations: { retry: false },
  },
})
