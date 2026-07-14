import { Alert } from '@snack-uikit/alert';
import { ButtonFilled, ButtonOutline } from '@snack-uikit/button';
import { ChipToggle } from '@snack-uikit/chips';
import { FieldSelect, FieldText } from '@snack-uikit/fields';
import { useMemo, useState } from 'react';

import { isAuditDisabledError } from '@/api/client';
import { useAuditRecords, useDataTypes } from '@/api/hooks';
import type { AuditQuery, AuditRecord } from '@/api/types';
import { DataGrid, type Column } from '@/components/DataGrid';
import { EntityChip } from '@/components/EntityChip';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { DATA_TYPE_NAME } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import { AuditDetailDrawer } from './AuditDetailDrawer';
import styles from './AuditPage.module.scss';

type ModeFilter = '' | 'enforce' | 'detect';

type Filters = {
  model: string;
  path: string;
  rule_id: string;
  data_type: number;
  mode: ModeFilter;
  since: string;
  until: string;
};

const EMPTY: Filters = {
  model: '',
  path: '',
  rule_id: '',
  data_type: 0,
  mode: '',
  since: '',
  until: '',
};

// Mode is filtered client-side (the API has no mode param) — it never enters the query.
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

type PresetKey = '1h' | '24h' | '7d' | 'all';

const RANGE_PRESETS: { key: PresetKey; label: string; ms: number | null }[] = [
  { key: '1h', label: t.audit.range.h1, ms: 3_600_000 },
  { key: '24h', label: t.audit.range.h24, ms: 24 * 3_600_000 },
  { key: '7d', label: t.audit.range.d7, ms: 7 * 24 * 3_600_000 },
  { key: 'all', label: t.audit.range.all, ms: null },
];

/** RFC3339 (state) → `datetime-local` value in the local clock, with seconds. */
function toLocalInput(rfc: string): string {
  if (!rfc) return '';
  const d = new Date(rfc);
  if (Number.isNaN(d.getTime())) return '';
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(
    d.getMinutes(),
  )}:${p(d.getSeconds())}`;
}

/**
 * `datetime-local` value → RFC3339 (UTC). Unparseable input is passed through
 * as-is — raw RFC3339 stays the advanced fallback the API accepts directly.
 */
function fromLocalInput(v: string): string {
  if (!v) return '';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? v : d.toISOString();
}

// The whole page speaks the operator's local clock: the datetime-local filter
// inputs, the clock column and the day-group headers — so a value the user
// just filtered on can never appear to violate the filter. The full RFC3339
// UTC timestamp stays available in the cell tooltip.
function formatClock(ts?: string): string {
  if (!ts) return t.common.none;
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts;
  const p = (n: number) => String(n).padStart(2, '0');
  const ms = String(d.getMilliseconds()).padStart(3, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${ms}`;
}

/** RFC3339 → local-day group key "YYYY-MM-DD". */
function localDayKey(ts?: string): string {
  if (!ts) return '';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return ts.slice(0, 10);
  const p = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

const DAY_FORMAT = new Intl.DateTimeFormat('ru-RU', {
  day: 'numeric',
  month: 'long',
  year: 'numeric',
});

/** Local "2026-07-14" → «14 июля 2026» (the trailing „г.“ is dropped). */
function formatDay(key: string): string {
  const d = new Date(`${key}T00:00:00`);
  if (Number.isNaN(d.getTime())) return key || t.common.none;
  return DAY_FORMAT.format(d).replace(/\s*г\.$/, '');
}

export function AuditPage() {
  const [filters, setFilters] = useState<Filters>(EMPTY);
  const [activePreset, setActivePreset] = useState<PresetKey | null>('all');
  // The last APPLIED filter set — the form (`filters`) diverges from it until
  // the user presses «Применить»; range presets only ever touch since/until.
  const [appliedFilters, setAppliedFilters] = useState<Filters>(EMPTY);
  const [selected, setSelected] = useState<AuditRecord | null>(null);

  const dataTypesQuery = useDataTypes();
  const applied = useMemo(() => toQuery(appliedFilters), [appliedFilters]);
  const appliedMode = appliedFilters.mode;
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
      { value: 0, option: t.audit.dataTypesAll },
      ...(dataTypesQuery.data ?? []).map((dt) => ({
        value: Number(dt.data_type),
        option: dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)] || String(dt.data_type),
      })),
    ],
    [dataTypesQuery.data],
  );

  const modeOptions = useMemo(
    () => [
      { value: '', option: t.audit.modeFilter.all },
      { value: 'enforce', option: 'enforce' },
      { value: 'detect', option: 'detect' },
    ],
    [],
  );

  const allRecords = useMemo(
    () => (audit.data?.pages ?? []).flatMap((p) => p.records ?? []),
    [audit.data],
  );

  const records = useMemo(
    () => (appliedMode ? allRecords.filter((r) => r.mode === appliedMode) : allRecords),
    [allRecords, appliedMode],
  );

  const set = <K extends keyof Filters>(key: K, value: Filters[K]) =>
    setFilters((prev) => ({ ...prev, [key]: value }));

  const setDate = (key: 'since' | 'until', value: string) => {
    set(key, value);
    setActivePreset(null);
  };

  // A preset applies ONLY the time range: the form fields update, but other
  // half-typed filters stay un-applied until the explicit «Применить».
  const applyPreset = (key: PresetKey) => {
    const preset = RANGE_PRESETS.find((p) => p.key === key);
    if (!preset) return;
    const since = preset.ms == null ? '' : new Date(Date.now() - preset.ms).toISOString();
    setFilters((prev) => ({ ...prev, since, until: '' }));
    setActivePreset(key);
    setAppliedFilters((prev) => ({ ...prev, since, until: '' }));
  };

  const apply = () => {
    setAppliedFilters(filters);
  };

  const reset = () => {
    setFilters(EMPTY);
    setActivePreset('all');
    setAppliedFilters(EMPTY);
  };

  const auditDisabled = isAuditDisabledError(audit.error);

  const rfc3339Title = (value: string) =>
    value ? `${value} — ${t.audit.rfc3339Title}` : t.audit.rfc3339Title;

  const columns: Column<AuditRecord>[] = [
    {
      key: 'timestamp',
      header: t.audit.col.timestamp,
      mono: true,
      width: '128px',
      render: (r) => <span title={r.timestamp}>{formatClock(r.timestamp)}</span>,
    },
    {
      key: 'model',
      header: t.audit.col.model,
      render: (r) => r.model || t.common.none,
    },
    {
      key: 'path',
      header: t.audit.col.path,
      mono: true,
      render: (r) => r.path || t.common.none,
    },
    {
      key: 'data_types',
      header: t.audit.col.dataTypes,
      render: (r) => {
        const ids = r.triggered_data_types ?? [];
        if (ids.length === 0) return <span className={styles.noneCell}>{t.common.none}</span>;
        const shown = ids.slice(0, 2);
        const rest = ids.slice(2);
        return (
          <div className={styles.chips}>
            {shown.map((id) => (
              <EntityChip key={id} dataType={id} size="s">
                {dtLabel(id)}
              </EntityChip>
            ))}
            {rest.length > 0 && (
              <span className={styles.moreChip} title={rest.map(dtLabel).join(', ')}>
                +{rest.length}
              </span>
            )}
          </div>
        );
      },
    },
    {
      key: 'replacements',
      header: t.audit.col.replacements,
      mono: true,
      align: 'right',
      width: '96px',
      render: (r) => (r.replacements ?? []).length,
    },
    {
      // Detect is the exception worth marking; enforce is the default and
      // stays quiet; an absent/unknown mode must NOT read as enforce.
      key: 'mode',
      header: t.audit.col.mode,
      width: '88px',
      render: (r) =>
        r.mode === 'detect' ? (
          <span className={styles.detectPill} title={t.audit.detectHint}>
            detect
          </span>
        ) : r.mode === 'enforce' ? null : (
          <span className={styles.noneCell}>{t.common.none}</span>
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
            <div className={styles.rangeChips} role="group" aria-label={t.audit.range.label}>
              {RANGE_PRESETS.map((p) => (
                <ChipToggle
                  key={p.key}
                  size="s"
                  label={p.label}
                  checked={activePreset === p.key}
                  onChange={() => applyPreset(p.key)}
                />
              ))}
            </div>

            <label className={styles.dateField}>
              <span className={styles.dateLabel}>{t.audit.sinceShort}</span>
              <input
                type="datetime-local"
                step={1}
                className={styles.dateInput}
                value={toLocalInput(filters.since)}
                title={rfc3339Title(filters.since)}
                onChange={(e) => setDate('since', fromLocalInput(e.target.value))}
              />
            </label>
            <label className={styles.dateField}>
              <span className={styles.dateLabel}>{t.audit.untilShort}</span>
              <input
                type="datetime-local"
                step={1}
                className={styles.dateInput}
                value={toLocalInput(filters.until)}
                title={rfc3339Title(filters.until)}
                onChange={(e) => setDate('until', fromLocalInput(e.target.value))}
              />
            </label>

            <div className={styles.field}>
              <FieldText
                inputMode="text"
                label={t.audit.filter.model}
                value={filters.model}
                onChange={(v) => set('model', v)}
              />
            </div>
            <div className={styles.field}>
              <FieldText
                inputMode="text"
                label={t.audit.filter.path}
                value={filters.path}
                onChange={(v) => set('path', v)}
              />
            </div>
            <div className={styles.field}>
              <FieldText
                inputMode="text"
                label={t.audit.filter.ruleId}
                value={filters.rule_id}
                onChange={(v) => set('rule_id', v)}
              />
            </div>
            <div className={styles.field}>
              <FieldSelect
                selection="single"
                label={t.audit.filter.dataType}
                options={dataTypeOptions}
                value={filters.data_type}
                onChange={(v) => set('data_type', v == null ? 0 : Number(v))}
              />
            </div>
            <div className={styles.fieldNarrow}>
              <FieldSelect
                selection="single"
                label={t.audit.modeFilter.label}
                options={modeOptions}
                value={filters.mode}
                onChange={(v) => set('mode', (v ?? '') as ModeFilter)}
              />
            </div>
          </div>

          <div className={styles.filterActions}>
            <ButtonFilled label={t.common.apply} onClick={apply} />
            <ButtonOutline label={t.common.cancel} onClick={reset} />
          </div>

          <QueryBoundary isLoading={audit.isLoading} error={audit.error} onRetry={() => audit.refetch()}>
            <DataGrid
              columns={columns}
              rows={records}
              rowKey={(r) => r.request_id ?? String(r.timestamp)}
              onRowClick={(r) => setSelected(r)}
              emptyLabel={
                // The mode filter is client-side over fetched pages: an empty
                // result does NOT mean matching records don't exist further on.
                appliedMode && (allRecords.length > 0 || audit.hasNextPage)
                  ? t.audit.modeClientHint
                  : t.audit.empty
              }
              groupBy={(r) => localDayKey(r.timestamp)}
              renderGroupHeader={formatDay}
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
