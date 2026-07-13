import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';

import { client, unwrap } from './client';
import type {
  AuditQuery,
  AuditRecord,
  AuditRecordList,
  BulkPatch,
  DataType,
  Rule,
  RuleSource,
  ScanRequest,
  Settings,
} from './types';

export const queryKeys = {
  rules: (source: RuleSource | 'all') => ['rules', source] as const,
  settings: ['settings'] as const,
  dataTypes: ['data-types'] as const,
  audit: (query: AuditQuery) => ['audit', query] as const,
  version: ['version'] as const,
  health: ['health'] as const,
  metricsSummary: ['metrics-summary'] as const,
};

// ---- Rules ----------------------------------------------------------------

export function useRules(source: RuleSource | 'all' = 'all') {
  return useQuery({
    queryKey: queryKeys.rules(source),
    queryFn: async () => {
      const res = await client.GET('/v1/rules', { params: { query: { source } } });
      return unwrap(res).rules ?? [];
    },
  });
}

export function useCreateRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (rule: Rule) => unwrap(await client.POST('/v1/rules', { body: rule })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  });
}

export function useUpdateRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (rule: Rule) =>
      unwrap(
        await client.PUT('/v1/rules/{id}', {
          params: { path: { id: rule.rule_id } },
          body: rule,
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  });
}

export function usePatchRuleEnabled() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, enabled }: { id: string; enabled: boolean }) =>
      unwrap(
        await client.PATCH('/v1/rules/{id}', {
          params: { path: { id } },
          body: { enabled },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  });
}

export function useBulkPatchRules() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: BulkPatch) => unwrap(await client.PATCH('/v1/rules', { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  });
}

export function useDeleteRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) =>
      unwrap(await client.DELETE('/v1/rules/{id}', { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['rules'] }),
  });
}

// ---- Settings -------------------------------------------------------------

export function useSettings() {
  return useQuery({
    queryKey: queryKeys.settings,
    queryFn: async () => unwrap(await client.GET('/v1/settings', {})),
  });
}

export function useUpdateSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (settings: Settings) =>
      unwrap(await client.PUT('/v1/settings', { body: settings })),
    onSuccess: (data) => qc.setQueryData(queryKeys.settings, data),
  });
}

// ---- Data types -----------------------------------------------------------

export function useDataTypes() {
  return useQuery({
    queryKey: queryKeys.dataTypes,
    queryFn: async (): Promise<DataType[]> => {
      const res = await client.GET('/v1/data-types', {});
      return unwrap(res).data_types ?? [];
    },
    staleTime: 5 * 60 * 1000,
  });
}

// ---- Audit ----------------------------------------------------------------

/** One-shot fetch of the most recent audit records for dashboard aggregation. */
export function useRecentAudit(limit = 200) {
  return useQuery({
    queryKey: ['audit-recent', limit],
    queryFn: async (): Promise<AuditRecord[]> => {
      const res = await client.GET('/v1/audit/records', { params: { query: { limit } } });
      return unwrap(res).records ?? [];
    },
    retry: false,
  });
}

export function useAuditRecords(query: AuditQuery) {
  return useInfiniteQuery({
    queryKey: queryKeys.audit(query),
    initialPageParam: undefined as string | undefined,
    queryFn: async ({ pageParam }): Promise<AuditRecordList> => {
      const res = await client.GET('/v1/audit/records', {
        params: { query: { ...query, cursor: pageParam } },
      });
      return unwrap(res);
    },
    getNextPageParam: (last) => last.next_cursor,
    retry: false,
  });
}

// ---- Scan (rule tester) ---------------------------------------------------

export function useScan() {
  return useMutation({
    mutationFn: async (body: ScanRequest) => unwrap(await client.POST('/v1/scan', { body })),
  });
}

// ---- Service status -------------------------------------------------------

export function useVersion() {
  return useQuery({
    queryKey: queryKeys.version,
    queryFn: async () => unwrap(await client.GET('/v1/version', {})),
    staleTime: 60 * 60 * 1000,
    retry: false,
  });
}

export function useHealth() {
  return useQuery({
    queryKey: queryKeys.health,
    queryFn: async () => unwrap(await client.GET('/v1/health', {})),
    refetchInterval: 15_000,
    staleTime: 0,
    retry: false,
  });
}

export function useMetricsSummary() {
  return useQuery({
    queryKey: queryKeys.metricsSummary,
    queryFn: async () => unwrap(await client.GET('/v1/metrics/summary', {})),
    staleTime: 30_000,
    retry: false,
  });
}

export type { AuditRecord };
