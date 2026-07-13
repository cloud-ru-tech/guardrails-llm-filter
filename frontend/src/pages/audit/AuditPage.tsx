import { Alert } from '@snack-uikit/alert';
import { ButtonFilled, ButtonOutline } from '@snack-uikit/button';
import { FieldSelect, FieldText } from '@snack-uikit/fields';
import { useMemo, useState } from 'react';

import { ApiRequestError } from '@/api/client';
import { useAuditRecords, useDataTypes } from '@/api/hooks';
import type { AuditQuery, AuditRecord } from '@/api/types';
import { Badge } from '@/components/Badge';
import { DataGrid, type Column } from '@/components/DataGrid';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { DATA_TYPE_NAME, DATA_TYPE_TONE } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import { AuditDetailDrawer } from './AuditDetailDrawer';
import styles from './AuditPage.module.scss';

type Filters = {
  model: string;
  path: string;
  rule_id: string;
  data_type: number;
  since: string;
  until: string;
};

const EMPTY: Filters = { model: '', path: '', rule_id: '', data_type: 0, since: '', until: '' };

function toQuery(f: Filters): AuditQuery {
  return {
    model: f.model || undefined,
    path: f.path || undefined,
    rule_id: f.rule_id || undefined,
    data_type: f.data_type ? String(f.data_type) : undefined,
    since: f.since || undefined,
    until: f.until || undefined,
    limit: 50,
  };
}

export function AuditPage() {
  const [filters, setFilters] = useState<Filters>(EMPTY);
  const [applied, setApplied] = useState<AuditQuery>(toQuery(EMPTY));
  const [selected, setSelected] = useState<AuditRecord | null>(null);

  const dataTypesQuery = useDataTypes();
  const audit = useAuditRecords(applied);

  const dataTypeLabels = useMemo(() => {
    const map = new Map<number, string>();
    for (const dt of dataTypesQuery.data ?? []) {
      map.set(Number(dt.data_type), dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)]);
    }
    return map;
  }, [dataTypesQuery.data]);

  const dtLabel = (id: number) => dataTypeLabels.get(id) ?? DATA_TYPE_NAME[id] ?? String(id);

  const dataTypeOptions = useMemo(
    () => [
      { value: 0, option: t.audit.filter.dataType },
      ...(dataTypesQuery.data ?? []).map((dt) => ({
        value: Number(dt.data_type),
        option: dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)] || String(dt.data_type),
      })),
    ],
    [dataTypesQuery.data],
  );

  const records = useMemo(
    () => (audit.data?.pages ?? []).flatMap((p) => p.records ?? []),
    [audit.data],
  );

  const set = <K extends keyof Filters>(key: K, value: Filters[K]) =>
    setFilters((prev) => ({ ...prev, [key]: value }));

  const auditDisabled = audit.error instanceof ApiRequestError && audit.error.status === 404;

  const columns: Column<AuditRecord>[] = [
    { key: 'timestamp', header: t.audit.col.timestamp, render: (r) => <span className={styles.mono}>{r.timestamp}</span> },
    { key: 'model', header: t.audit.col.model, render: (r) => r.model || t.common.none },
    { key: 'path', header: t.audit.col.path, render: (r) => <span className={styles.mono}>{r.path}</span> },
    {
      key: 'mode',
      header: t.audit.col.mode,
      render: (r) => <Badge tone={r.mode === 'detect' ? 'yellow' : 'green'}>{r.mode ?? '—'}</Badge>,
    },
    {
      key: 'rules',
      header: t.audit.col.rules,
      render: (r) => (r.triggered_rule_ids ?? []).length,
      align: 'center',
    },
    {
      key: 'data_types',
      header: t.audit.col.dataTypes,
      render: (r) => (
        <div className={styles.chips}>
          {(r.triggered_data_types ?? []).map((id) => (
            <Badge key={id} tone={DATA_TYPE_TONE[id] ?? 'neutral'}>
              {dtLabel(id)}
            </Badge>
          ))}
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader title={t.audit.title} description={t.audit.description} />

      {auditDisabled ? (
        <Alert appearance="info" icon title={t.audit.disabledTitle} description={t.audit.disabledHint} />
      ) : (
        <>
          <div className={styles.filters}>
            <FieldText inputMode="text" label={t.audit.filter.model} value={filters.model} onChange={(v) => set('model', v)} />
            <FieldText inputMode="text" label={t.audit.filter.path} value={filters.path} onChange={(v) => set('path', v)} />
            <FieldText inputMode="text" label={t.audit.filter.ruleId} value={filters.rule_id} onChange={(v) => set('rule_id', v)} />
            <FieldSelect
              selection="single"
              label={t.audit.filter.dataType}
              options={dataTypeOptions}
              value={filters.data_type}
              onChange={(v) => set('data_type', v == null ? 0 : Number(v))}
            />
            <FieldText inputMode="text" label={t.audit.filter.since} value={filters.since} onChange={(v) => set('since', v)} placeholder="2026-01-01T00:00:00Z" />
            <FieldText inputMode="text" label={t.audit.filter.until} value={filters.until} onChange={(v) => set('until', v)} placeholder="2026-12-31T23:59:59Z" />
          </div>
          <div className={styles.filterActions}>
            <ButtonFilled label={t.common.apply} onClick={() => setApplied(toQuery(filters))} />
            <ButtonOutline
              label={t.common.cancel}
              onClick={() => {
                setFilters(EMPTY);
                setApplied(toQuery(EMPTY));
              }}
            />
          </div>

          <QueryBoundary isLoading={audit.isLoading} error={audit.error} onRetry={() => audit.refetch()}>
            <DataGrid
              columns={columns}
              rows={records}
              rowKey={(r) => r.request_id ?? String(r.timestamp)}
              onRowClick={(r) => setSelected(r)}
              emptyLabel={t.audit.empty}
            />
            {audit.hasNextPage && (
              <div className={styles.loadMore}>
                <ButtonOutline
                  label={t.common.loadMore}
                  loading={audit.isFetchingNextPage}
                  onClick={() => audit.fetchNextPage()}
                />
              </div>
            )}
          </QueryBoundary>
        </>
      )}

      <AuditDetailDrawer record={selected} onClose={() => setSelected(null)} dtLabel={dtLabel} />
    </div>
  );
}
