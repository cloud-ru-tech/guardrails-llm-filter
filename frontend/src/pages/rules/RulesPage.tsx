import { ButtonFilled, ButtonFunction, ButtonOutline } from '@snack-uikit/button';
import { FieldSelect, FieldText } from '@snack-uikit/fields';
import {
  DownloadSVG,
  FunctionSettingsSVG,
  PlusSVG,
  SearchSVG,
  TrashSVG,
  UploadSVG,
} from '@snack-uikit/icons';
import { SegmentedControl } from '@snack-uikit/segmented-control';
import { toaster } from '@snack-uikit/toaster';
import { Checkbox, Switch } from '@snack-uikit/toggles';
import { useMemo, useRef, useState, type ChangeEvent } from 'react';

import {
  useBulkPatchRules,
  useCreateRule,
  useDataTypes,
  useDeleteRule,
  usePatchRuleEnabled,
  useRules,
} from '@/api/hooks';
import type { Rule, RuleSource } from '@/api/types';
import { Badge } from '@/components/Badge';
import { Card } from '@/components/Card';
import { DataGrid, type Column } from '@/components/DataGrid';
import { EntityChip } from '@/components/EntityChip';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { CHART_COLOR, DATA_TYPE_NAME } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import pageStyles from '@/components/Page.module.scss';
import { RuleFormDrawer } from './RuleFormDrawer';
import styles from './RulesPage.module.scss';

type SourceFilter = RuleSource | 'all';
type ViewMode = 'table' | 'effective';

/** Colored entity dot for select options — always next to the text label. */
const entityDot = (id: number) => (
  <span
    className={styles.entityDot}
    style={{ background: CHART_COLOR[id] ?? 'var(--sys-neutral-text-support)' }}
    aria-hidden="true"
  />
);

export function RulesPage() {
  const [source, setSource] = useState<SourceFilter>('all');
  const [dataTypeFilter, setDataTypeFilter] = useState<number>(0);
  const [search, setSearch] = useState('');
  const [view, setView] = useState<ViewMode>('table');
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editing, setEditing] = useState<Rule | undefined>(undefined);
  const [patchingId, setPatchingId] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const fileInputRef = useRef<HTMLInputElement>(null);

  const rulesQuery = useRules(source);
  const allRulesQuery = useRules('all');
  const customRulesQuery = useRules('custom');
  const dataTypesQuery = useDataTypes();
  const patchEnabled = usePatchRuleEnabled();
  const deleteRule = useDeleteRule();
  const createRule = useCreateRule();
  const bulkPatch = useBulkPatchRules();

  const dataTypeLabels = useMemo(() => {
    const map = new Map<number, string>();
    for (const dt of dataTypesQuery.data ?? []) {
      map.set(Number(dt.data_type), dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)]);
    }
    return map;
  }, [dataTypesQuery.data]);

  const dataTypeOptions = useMemo(
    () =>
      (dataTypesQuery.data ?? []).map((dt) => ({
        value: Number(dt.data_type),
        option: dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)] || String(dt.data_type),
      })),
    [dataTypesQuery.data],
  );

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase();
    return (rulesQuery.data ?? []).filter((r) => {
      if (dataTypeFilter && r.data_type !== dataTypeFilter) return false;
      if (q && !(`${r.rule_id} ${r.name}`.toLowerCase().includes(q))) return false;
      return true;
    });
  }, [rulesQuery.data, dataTypeFilter, search]);

  const dtLabel = (id: number) => dataTypeLabels.get(id) ?? DATA_TYPE_NAME[id] ?? String(id);

  // Effective ruleset: builtin ∪ custom − disabled, grouped by data type.
  const effectiveGroups = useMemo(() => {
    const groups = new Map<number, Rule[]>();
    for (const r of allRulesQuery.data ?? []) {
      if (r.enabled === false) continue;
      const arr = groups.get(r.data_type) ?? [];
      arr.push(r);
      groups.set(r.data_type, arr);
    }
    return [...groups.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([id, groupRules]) => ({ id, rules: groupRules }));
  }, [allRulesQuery.data]);

  const handleToggle = (rule: Rule, enabled: boolean) => {
    setPatchingId(rule.rule_id);
    patchEnabled.mutate(
      { id: rule.rule_id, enabled },
      {
        onError: () => toaster.userAction.error({ label: t.rules.enableError }),
        onSettled: () => setPatchingId(null),
      },
    );
  };

  const handleDelete = (rule: Rule) => {
    if (!window.confirm(t.rules.deleteConfirm)) return;
    deleteRule.mutate(rule.rule_id, {
      onSuccess: () => toaster.userAction.success({ label: t.rules.deleted }),
      onError: () => toaster.userAction.error({ label: t.rules.enableError }),
    });
  };

  const openCreate = () => {
    setEditing(undefined);
    setDrawerOpen(true);
  };
  const openEdit = (rule: Rule) => {
    setEditing(rule);
    setDrawerOpen(true);
  };

  // ---- Export / Import ----------------------------------------------------

  const handleExport = () => {
    const custom = customRulesQuery.data ?? [];
    const blob = new Blob([JSON.stringify(custom, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'guardrails-custom-rules.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const handleImportFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = ''; // allow re-selecting the same file
    if (!file) return;

    let parsed: unknown;
    try {
      parsed = JSON.parse(await file.text());
    } catch {
      toaster.userAction.error({ label: t.rules.importBadFile });
      return;
    }
    if (!Array.isArray(parsed)) {
      toaster.userAction.error({ label: t.rules.importBadFile });
      return;
    }

    let ok = 0;
    let fail = 0;
    for (const rule of parsed) {
      try {
        await createRule.mutateAsync(rule as Rule);
        ok += 1;
      } catch {
        fail += 1;
      }
    }
    const notify = fail === 0 ? toaster.userAction.success : toaster.userAction.error;
    notify({ label: t.rules.importDone(ok, fail) });
  };

  // ---- Bulk enable / disable ---------------------------------------------

  const toggleRowSelected = (id: string, checked: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });

  const allVisibleSelected = rows.length > 0 && rows.every((r) => selected.has(r.rule_id));
  const someVisibleSelected = rows.some((r) => selected.has(r.rule_id));

  const toggleAllVisible = (checked: boolean) =>
    setSelected((prev) => {
      const next = new Set(prev);
      for (const r of rows) {
        if (checked) next.add(r.rule_id);
        else next.delete(r.rule_id);
      }
      return next;
    });

  const handleBulk = (enabled: boolean) => {
    const ids = [...selected];
    if (ids.length === 0) return;
    bulkPatch.mutate(
      { ids, enabled },
      {
        onSuccess: (res) => {
          const results = res.results ?? [];
          const okCount = results.filter((r) => r.status === 'ok').length;
          const failCount = results.length - okCount;
          const notify = failCount === 0 ? toaster.userAction.success : toaster.userAction.error;
          notify({ label: t.rules.bulkDone(okCount, failCount) });
          setSelected(new Set());
        },
        onError: () => toaster.userAction.error({ label: t.rules.enableError }),
      },
    );
  };

  const columns: Column<Rule>[] = [
    {
      key: 'select',
      header: (
        <Checkbox
          checked={allVisibleSelected}
          indeterminate={someVisibleSelected && !allVisibleSelected}
          onChange={toggleAllVisible}
          aria-label={t.rules.selectAll}
        />
      ),
      width: '44px',
      align: 'center',
      render: (r) => (
        <Checkbox
          checked={selected.has(r.rule_id)}
          onChange={(checked) => toggleRowSelected(r.rule_id, checked)}
          aria-label={t.rules.selectRow}
        />
      ),
    },
    {
      key: 'rule',
      header: t.rules.col.rule,
      render: (r) => {
        // Builtin rules ship name === rule_id: one mono line. Custom rules show
        // the human name with the mono id as a support second line.
        const hasOwnName = Boolean(r.name) && r.name !== r.rule_id;
        // The group is only worth a suffix when it adds information: hide it
        // when the id prefix already says it ("CREDENTIALS" vs "credentials.*",
        // case-insensitive) and when it merely repeats the data type shown as
        // a chip in the next column ("PERSONAL_DATA" for pii.* rules).
        const group = r.group?.toLowerCase();
        const showGroup =
          Boolean(group) &&
          !r.rule_id.toLowerCase().startsWith(group!) &&
          group !== DATA_TYPE_NAME[r.data_type]?.toLowerCase();
        return (
          <div className={styles.ruleCell}>
            {hasOwnName && <span className={styles.ruleName}>{r.name}</span>}
            <span className={hasOwnName ? `${styles.ruleId} ${styles.ruleIdSub}` : styles.ruleId}>
              {r.rule_id}
              {showGroup && <span className={styles.ruleGroup}> · {r.group}</span>}
            </span>
          </div>
        );
      },
    },
    {
      key: 'data_type',
      header: t.rules.col.dataType,
      render: (r) => <EntityChip dataType={r.data_type}>{dtLabel(r.data_type)}</EntityChip>,
    },
    {
      key: 'source',
      header: t.rules.col.source,
      render: (r) =>
        r.source === 'custom' ? (
          <Badge tone="blue">{t.rules.sourceCustomBadge}</Badge>
        ) : (
          <Badge tone="neutral">{t.rules.sourceBuiltinBadge}</Badge>
        ),
    },
    {
      key: 'enabled',
      header: <span className={styles.nowrap}>{t.rules.col.enabled}</span>,
      align: 'center',
      width: '96px',
      render: (r) => (
        <Switch
          checked={r.enabled ?? true}
          loading={patchingId === r.rule_id}
          onChange={(checked) => handleToggle(r, checked)}
        />
      ),
    },
    {
      key: 'actions',
      header: '',
      width: '88px',
      render: (r) =>
        r.source === 'custom' ? (
          <div className={styles.actions}>
            <ButtonFunction
              icon={<FunctionSettingsSVG />}
              aria-label={t.common.edit}
              onClick={() => openEdit(r)}
            />
            <ButtonFunction
              icon={<TrashSVG />}
              aria-label={t.common.delete}
              onClick={() => handleDelete(r)}
            />
          </div>
        ) : null,
    },
  ];

  return (
    <div>
      <PageHeader
        title={t.rules.title}
        description={t.rules.description}
        actions={
          <>
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json,.json"
              className={styles.hiddenInput}
              onChange={handleImportFile}
            />
            <ButtonOutline
              label={t.rules.import}
              icon={<UploadSVG />}
              loading={createRule.isPending}
              onClick={() => fileInputRef.current?.click()}
            />
            <ButtonOutline
              label={t.rules.export}
              icon={<DownloadSVG />}
              disabled={(customRulesQuery.data ?? []).length === 0}
              onClick={handleExport}
            />
            <ButtonFilled label={t.rules.create} icon={<PlusSVG />} onClick={openCreate} />
          </>
        }
      />

      <div className={pageStyles.toolbar}>
        <SegmentedControl<ViewMode>
          value={view}
          onChange={setView}
          items={[
            { value: 'table', label: t.rules.viewTable },
            { value: 'effective', label: t.rules.viewEffective },
          ]}
        />
        {view === 'table' && (
          <>
            <div className={styles.filterSource}>
              <SegmentedControl<SourceFilter>
                value={source}
                onChange={setSource}
                items={[
                  { value: 'all', label: t.rules.sourceAll },
                  { value: 'builtin', label: t.rules.sourceBuiltin },
                  { value: 'custom', label: t.rules.sourceCustom },
                ]}
              />
            </div>
            <div className={styles.filterDataType}>
              <FieldSelect
                selection="single"
                value={dataTypeFilter}
                onChange={(v) => setDataTypeFilter(v == null ? 0 : Number(v))}
                options={[
                  { value: 0, option: t.rules.dataTypeAll },
                  ...dataTypeOptions.map((o) => ({ ...o, beforeContent: entityDot(o.value) })),
                ]}
              />
            </div>
            <div className={styles.filterSearch}>
              <FieldText
                inputMode="text"
                value={search}
                onChange={setSearch}
                placeholder={t.rules.searchPlaceholder}
                prefixIcon={<SearchSVG />}
              />
            </div>
            <span className={styles.count}>{t.rules.counter(rows.length)}</span>
          </>
        )}
      </div>

      {view === 'effective' ? (
        <QueryBoundary
          isLoading={allRulesQuery.isLoading}
          error={allRulesQuery.error}
          onRetry={() => allRulesQuery.refetch()}
        >
          <Card title={t.rules.effective.title} subtitle={t.rules.effective.subtitle}>
            {effectiveGroups.length === 0 ? (
              <span className={styles.mutedText}>{t.rules.effective.empty}</span>
            ) : (
              <div className={styles.effectiveGroups}>
                {effectiveGroups.map((group) => (
                  <div key={group.id} className={styles.effectiveGroup}>
                    <div className={styles.effectiveHead}>
                      <EntityChip dataType={group.id}>{dtLabel(group.id)}</EntityChip>
                      <span className={styles.effectiveCount}>{t.rules.counter(group.rules.length)}</span>
                    </div>
                    <div className={styles.effectiveChips}>
                      {group.rules.map((r) => (
                        <span
                          key={r.rule_id}
                          className={styles.effectiveChip}
                          title={r.name && r.name !== r.rule_id ? r.name : undefined}
                        >
                          {r.rule_id}
                        </span>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </QueryBoundary>
      ) : (
        <>
          {selected.size > 0 && (
            <div className={styles.bulkBar}>
              <span className={styles.bulkLabel}>
                {t.rules.bulkSelectedLabel}: <span className={styles.bulkCount}>{selected.size}</span>
              </span>
              <div className={styles.bulkActions}>
                <ButtonOutline
                  label={t.rules.bulkEnable}
                  loading={bulkPatch.isPending}
                  onClick={() => handleBulk(true)}
                />
                <ButtonOutline
                  label={t.rules.bulkDisable}
                  loading={bulkPatch.isPending}
                  onClick={() => handleBulk(false)}
                />
              </div>
            </div>
          )}

          <QueryBoundary
            isLoading={rulesQuery.isLoading}
            error={rulesQuery.error}
            onRetry={() => rulesQuery.refetch()}
          >
            <DataGrid
              columns={columns}
              rows={rows}
              rowKey={(r) => r.rule_id}
              emptyLabel={t.rules.empty}
            />
          </QueryBoundary>
        </>
      )}

      <RuleFormDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        rule={editing}
        dataTypeOptions={dataTypeOptions}
      />
    </div>
  );
}
