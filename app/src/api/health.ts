import { useQuery } from '@tanstack/react-query';

import { apiClient, callApi } from './client';
import { queryKeys } from './query-keys';

/** `GET /health`: whether the API and its database are up. */
export function useHealthQuery() {
  return useQuery({
    queryKey: queryKeys.health(),
    queryFn: ({ signal }) => callApi(() => apiClient.GET('/health', { signal })),
  });
}
