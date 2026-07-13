import { Alert } from '@snack-uikit/alert';
import { ButtonOutline } from '@snack-uikit/button';
import { UpdateSVG } from '@snack-uikit/icons';
import { useMemo } from 'react';
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
  type TooltipProps,
} from 'recharts';

import { ApiRequestError } from '@/api/client';
import { useDataTypes, useMetricsSummary, useRecentAudit } from '@/api/hooks';
import type { Latency } from '@/api/types';
import { Badge } from '@/components/Badge';
import { Card } from '@/components/Card';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { StatTile } from '@/components/StatTile';
import { CHART_COLOR, DATA_TYPE_NAME, DATA_TYPE_ORDER, DATA_TYPE_TONE } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './OverviewPage.module.scss';

const WINDOW = 200;
const AXIS = { fill: 'var(--chart-axis)', fontSize: 12 };

function ChartTooltip({ active, payload, label }: TooltipProps<number, string>) {
  if (!active || !payload?.length) return null;
  return (
    <div className={styles.tooltip}>
      {label != null && <div className={styles.tooltipLabel}>{label}</div>}
      {payload.map((p) => (
        <div key={String(p.name)}>
          {p.name}: <strong>{p.value}</strong>
        </div>
      ))}
    </div>
  );
}

/** Lifetime service counters from GET /v1/metrics/summary (independent of audit). */
function LifetimeMetrics({ dtLabel }: { dtLabel: (id: number) => string }) {
  const metrics = useMetricsSummary();
  const data = metrics.data;
  // Silently skip when metrics are unavailable — this only augments the page.
  if (metrics.isError || !data) return null;

  const maskedByMode = data.requests_masked_total ?? {};
  const maskedTotal = Object.values(maskedByMode).reduce((a, b) => a + b, 0);
  const enforce = maskedByMode.enforce ?? 0;
  const detect = maskedByMode.detect ?? 0;
  const passthrough = Object.values(data.passthrough_total ?? {}).reduce((a, b) => a + b, 0);

  // Use the highest-volume latency bucket as the representative sample.
  const latency = Object.values(data.latency_seconds ?? {}).reduce<Latency | null>(
    (best, l) => (best == null || (l.count ?? 0) > (best.count ?? 0) ? l : best),
    null,
  );
  const toMs = (s?: number) => (s == null ? null : `${Math.round(s * 1000)} ms`);
  const p50 = toMs(latency?.p50);
  const p95 = toMs(latency?.p95);

  const topRules = [...(data.rule_triggers_total ?? [])]
    .sort((a, b) => (b.count ?? 0) - (a.count ?? 0))
    .slice(0, 6);
  const topDataTypes = [...(data.data_type_triggers_total ?? [])]
    .sort((a, b) => (b.count ?? 0) - (a.count ?? 0))
    .slice(0, 6);

  return (
    <div className={styles.lifetime}>
      <div className={styles.statGrid}>
        <StatTile label={t.overview.lifetime.maskedTotal} value={maskedTotal} accent />
        <StatTile label={t.overview.lifetime.enforce} value={enforce} />
        <StatTile label={t.overview.lifetime.detect} value={detect} />
        <StatTile label={t.overview.lifetime.passthrough} value={passthrough} />
        <StatTile label={t.overview.lifetime.latencyP50} value={p50 ?? t.common.none} />
        <StatTile label={t.overview.lifetime.latencyP95} value={p95 ?? t.common.none} />
      </div>

      <div className={styles.lifetimeLists}>
        <Card title={t.overview.lifetime.topRules}>
          {topRules.length === 0 ? (
            <span className={styles.muted}>{t.overview.lifetime.empty}</span>
          ) : (
            <ul className={styles.countList}>
              {topRules.map((r) => (
                <li key={r.label} className={styles.countRow}>
                  <span className={styles.mono}>{r.label}</span>
                  <span className={styles.countValue}>{r.count ?? 0}</span>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <Card title={t.overview.lifetime.topDataTypes}>
          {topDataTypes.length === 0 ? (
            <span className={styles.muted}>{t.overview.lifetime.empty}</span>
          ) : (
            <ul className={styles.countList}>
              {topDataTypes.map((d) => {
                const id = Number(d.label);
                const label = Number.isFinite(id) ? dtLabel(id) : String(d.label);
                return (
                  <li key={d.label} className={styles.countRow}>
                    <Badge tone={DATA_TYPE_TONE[id] ?? 'neutral'}>{label}</Badge>
                    <span className={styles.countValue}>{d.count ?? 0}</span>
                  </li>
                );
              })}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}

export function OverviewPage() {
  const audit = useRecentAudit(WINDOW);
  const dataTypesQuery = useDataTypes();

  const labels = useMemo(() => {
    const map = new Map<number, string>();
    for (const dt of dataTypesQuery.data ?? []) {
      map.set(Number(dt.data_type), dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)]);
    }
    return map;
  }, [dataTypesQuery.data]);
  const dtLabel = (id: number) => labels.get(id) ?? DATA_TYPE_NAME[id] ?? String(id);

  const agg = useMemo(() => {
    const records = audit.data ?? [];
    const byDataType = new Map<number, number>();
    const byRule = new Map<string, number>();
    const byHour = new Map<string, number>();
    let enforce = 0;
    let detect = 0;
    let replacements = 0;

    for (const r of records) {
      if (r.mode === 'detect') detect += 1;
      else enforce += 1;
      replacements += r.replacements?.length ?? 0;
      for (const id of r.triggered_data_types ?? []) byDataType.set(id, (byDataType.get(id) ?? 0) + 1);
      for (const id of r.triggered_rule_ids ?? []) byRule.set(id, (byRule.get(id) ?? 0) + 1);
      if (r.timestamp) {
        const d = new Date(r.timestamp);
        if (!Number.isNaN(d.getTime())) {
          const key = `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}:00`;
          byHour.set(key, (byHour.get(key) ?? 0) + 1);
        }
      }
    }

    const dataTypeData = DATA_TYPE_ORDER.filter((id) => byDataType.has(id)).map((id) => ({
      id,
      name: dtLabel(id),
      value: byDataType.get(id) ?? 0,
    }));

    const topRules = [...byRule.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, 8)
      .map(([name, value]) => ({ name, value }));

    const overTime = [...byHour.entries()]
      .sort((a, b) => a[0].localeCompare(b[0]))
      .map(([time, count]) => ({ time, count }));

    return {
      total: records.length,
      enforce,
      detect,
      replacements,
      distinctRules: byRule.size,
      distinctDataTypes: byDataType.size,
      dataTypeData,
      topRules,
      overTime,
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [audit.data, labels]);

  const auditDisabled = audit.error instanceof ApiRequestError && audit.error.status === 404;

  if (auditDisabled) {
    return (
      <div>
        <PageHeader title={t.overview.title} description={t.overview.description} />
        <LifetimeMetrics dtLabel={dtLabel} />
        <Alert appearance="info" icon title={t.overview.disabledTitle} description={t.overview.disabledHint} />
      </div>
    );
  }

  const enforcePct = agg.total ? Math.round((agg.enforce / agg.total) * 100) : 0;

  return (
    <div>
      <PageHeader
        title={t.overview.title}
        description={t.overview.description}
        actions={
          <ButtonOutline
            label={t.overview.refresh}
            icon={<UpdateSVG />}
            loading={audit.isFetching}
            onClick={() => audit.refetch()}
          />
        }
      />

      <LifetimeMetrics dtLabel={dtLabel} />

      <QueryBoundary isLoading={audit.isLoading} error={audit.error} onRetry={() => audit.refetch()}>
        {agg.total === 0 ? (
          <Alert appearance="info" icon description={t.overview.empty} />
        ) : (
          <>
            <div className={styles.statGrid}>
              <StatTile
                label={t.overview.stat.total}
                value={agg.total}
                hint={t.overview.windowHint(WINDOW)}
                accent
              />
              <StatTile label={t.overview.stat.dataTypes} value={agg.distinctDataTypes} />
              <StatTile label={t.overview.stat.rules} value={agg.distinctRules} />
              <StatTile label={t.overview.stat.replacements} value={agg.replacements} />
            </div>

            <div className={styles.chartGrid}>
              <Card title={t.overview.chart.byDataType} subtitle={t.overview.chart.byDataTypeSub}>
                <ResponsiveContainer width="100%" height={280}>
                  <BarChart data={agg.dataTypeData} margin={{ top: 8, right: 8, bottom: 8, left: -8 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" vertical={false} />
                    <XAxis dataKey="name" tick={AXIS} tickLine={false} axisLine={{ stroke: 'var(--chart-grid)' }} interval={0} angle={-15} textAnchor="end" height={60} />
                    <YAxis allowDecimals={false} tick={AXIS} tickLine={false} axisLine={false} width={40} />
                    <Tooltip content={<ChartTooltip />} cursor={{ fill: 'var(--sys-neutral-background-opacity)' }} />
                    <Bar dataKey="value" name={t.overview.chart.events} radius={[6, 6, 0, 0]}>
                      {agg.dataTypeData.map((d) => (
                        <Cell key={d.id} fill={CHART_COLOR[d.id] ?? 'var(--chart-dt-6)'} />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </Card>

              <Card title={t.overview.chart.topRules} subtitle={t.overview.chart.topRulesSub}>
                <ResponsiveContainer width="100%" height={280}>
                  <BarChart
                    layout="vertical"
                    data={agg.topRules}
                    margin={{ top: 4, right: 12, bottom: 4, left: 8 }}
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" horizontal={false} />
                    <XAxis type="number" allowDecimals={false} tick={AXIS} tickLine={false} axisLine={{ stroke: 'var(--chart-grid)' }} />
                    <YAxis type="category" dataKey="name" tick={AXIS} tickLine={false} axisLine={false} width={150} />
                    <Tooltip content={<ChartTooltip />} cursor={{ fill: 'var(--sys-neutral-background-opacity)' }} />
                    <Bar dataKey="value" name={t.overview.chart.events} fill="var(--chart-primary)" radius={[0, 6, 6, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </Card>

              <Card
                title={t.overview.chart.overTime}
                subtitle={t.overview.chart.overTimeSub}
                className={styles.full}
              >
                <div>
                  <ResponsiveContainer width="100%" height={240}>
                    <AreaChart data={agg.overTime} margin={{ top: 8, right: 8, bottom: 8, left: -8 }}>
                      <defs>
                        <linearGradient id="ov-area" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0%" stopColor="var(--chart-primary)" stopOpacity={0.35} />
                          <stop offset="100%" stopColor="var(--chart-primary)" stopOpacity={0.02} />
                        </linearGradient>
                      </defs>
                      <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" vertical={false} />
                      <XAxis dataKey="time" tick={AXIS} tickLine={false} axisLine={{ stroke: 'var(--chart-grid)' }} minTickGap={24} />
                      <YAxis allowDecimals={false} tick={AXIS} tickLine={false} axisLine={false} width={40} />
                      <Tooltip content={<ChartTooltip />} />
                      <Area
                        type="monotone"
                        dataKey="count"
                        name={t.overview.chart.events}
                        stroke="var(--chart-primary)"
                        strokeWidth={2}
                        fill="url(#ov-area)"
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                </div>
              </Card>

              <Card title={t.overview.chart.modeSplit} className={styles.full}>
                <div className={styles.modeBar}>
                  <div className={styles.modeEnforce} style={{ width: `${enforcePct}%` }} />
                  <div className={styles.modeDetect} style={{ width: `${100 - enforcePct}%` }} />
                </div>
                <div className={styles.modeLegend}>
                  <span>
                    {t.overview.stat.enforce}: {agg.enforce}
                  </span>
                  <span>
                    {t.overview.stat.detect}: {agg.detect}
                  </span>
                </div>
              </Card>
            </div>
          </>
        )}
      </QueryBoundary>
    </div>
  );
}
