import { Alert } from '@snack-uikit/alert';
import { ButtonOutline } from '@snack-uikit/button';
import { UpdateSVG } from '@snack-uikit/icons';
import { useMemo } from 'react';
import { Link } from 'react-router-dom';
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

import { isAuditDisabledError } from '@/api/client';
import { useDataTypes, useMetricsSummary, useRecentAudit } from '@/api/hooks';
import type { AuditRecord, Latency } from '@/api/types';
import { Card } from '@/components/Card';
import { MaskChip } from '@/components/MaskedText';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { Readout } from '@/components/StatTile';
import { CHART_COLOR, DATA_TYPE_NAME, DATA_TYPE_ORDER } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './OverviewPage.module.scss';

const WINDOW = 200;
const RECENT_COUNT = 5;

// Axes speak mono, like every number on this console.
const AXIS = {
  fill: 'var(--chart-axis)',
  fontSize: 12,
  fontFamily: 'var(--mono-body-s-font-family, ui-monospace, monospace)',
};

const nf = new Intl.NumberFormat('ru-RU');
const fmt = (n: number) => nf.format(n);

// NBSP instead of spaces: recharts' <Text> otherwise wraps a two-word label
// onto a second line inside the 140px axis; the full name lives in the tooltip.
const ellipsize = (v: string) =>
  (v.length > 18 ? `${v.slice(0, 17)}…` : v).replace(/ /g, ' ');

function fmtTime(ts?: string): string {
  if (!ts) return t.common.none;
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return t.common.none;
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

/**
 * Adaptive latency format: sub-millisecond quantiles read «<1 мс». A bucket
 * with zero samples yields null — the histogram always exists, so without the
 * count guard a fresh instance would fabricate a measured «<1 мс».
 */
function fmtLatency(seconds?: number, count?: number): string | null {
  if (!count || seconds == null) return null;
  const ms = seconds * 1000;
  return ms < 1 ? t.overview.hero.subMs : t.overview.hero.ms(Math.round(ms));
}

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

type AuditAgg = {
  total: number;
  enforce: number;
  detect: number;
  replacements: number;
  dataTypeData: { id: number; name: string; value: number }[];
  topRules: { name: string; value: number }[];
  overTime: { time: string; count: number }[];
};

/**
 * HERO: the lifetime instrument panel (GET /v1/metrics/summary). Degrades but
 * never disappears — when metrics are unavailable it recomputes from the audit
 * window (eyebrow says so); when both are unavailable it shows em-dashes.
 */
function Hero({
  metrics,
  agg,
  auditAvailable,
}: {
  metrics: ReturnType<typeof useMetricsSummary>;
  agg: AuditAgg;
  auditAvailable: boolean;
}) {
  const m = metrics.data;
  const metricsSettled = !metrics.isPending;
  const base: 'metrics' | 'audit' | 'none' = m
    ? 'metrics'
    : metricsSettled && auditAvailable
      ? 'audit'
      : 'none';

  let masked: number | null = null;
  let enforce: number | null = null;
  let detect: number | null = null;
  let p50: string | null = null;
  let p95: string | null = null;
  let latencyHint: string | undefined;
  let passthrough: number | null = null;
  let passthroughHint: string | undefined;
  let replacements: number | null = null;

  if (base === 'metrics' && m) {
    const byMode = m.requests_masked_total ?? {};
    masked = Object.values(byMode).reduce((a, b) => a + b, 0);
    enforce = byMode.enforce ?? 0;
    detect = byMode.detect ?? 0;
    passthrough = Object.values(m.passthrough_total ?? {}).reduce((a, b) => a + b, 0);
    // No replacements counter exists in the metrics summary (rule_triggers_total
    // counts per-request triggers and is capped to the top 20) — the readout is
    // shown only when the hero is computed from the audit window.

    // The highest-volume latency bucket is the representative sample.
    const bucket = Object.entries(m.latency_seconds ?? {}).reduce<[string, Latency] | null>(
      (best, cur) => (best == null || (cur[1].count ?? 0) > (best[1].count ?? 0) ? cur : best),
      null,
    );
    p50 = fmtLatency(bucket?.[1].p50, bucket?.[1].count);
    p95 = fmtLatency(bucket?.[1].p95, bucket?.[1].count);
    latencyHint =
      bucket && (p50 != null || p95 != null)
        ? t.overview.hero.latencyBucket(bucket[0])
        : t.overview.hero.noTraffic;
  } else if (base === 'audit') {
    masked = agg.total;
    enforce = agg.enforce;
    detect = agg.detect;
    replacements = agg.replacements;
    latencyHint = t.overview.hero.noMetrics;
    passthroughHint = t.overview.hero.noMetrics;
  } else {
    latencyHint = t.overview.hero.noMetrics;
    passthroughHint = t.overview.hero.noMetrics;
  }

  const splitTotal = (enforce ?? 0) + (detect ?? 0);

  return (
    <Card
      eyebrow={
        base === 'audit' ? t.overview.hero.eyebrowWindow(WINDOW) : t.overview.hero.eyebrowLifetime
      }
      className={styles.heroCard}
    >
      <div className={styles.heroBody}>
        <div className={styles.heroMain}>
          <span className={styles.heroLabel}>{t.overview.hero.masked}</span>
          <span className={styles.heroValue}>{masked == null ? t.common.none : fmt(masked)}</span>
        </div>

        <div className={styles.heroSplit}>
          <div className={styles.splitCounts}>
            <span className={styles.splitPair}>
              <span className={styles.splitNum}>{enforce == null ? t.common.none : fmt(enforce)}</span>{' '}
              {t.overview.hero.enforce}
            </span>
            <span aria-hidden="true">·</span>
            <span className={styles.splitPair}>
              <span className={styles.splitNum}>{detect == null ? t.common.none : fmt(detect)}</span>{' '}
              {t.overview.hero.detect}
            </span>
          </div>
          <div className={styles.splitBar} aria-hidden="true">
            {splitTotal > 0 && (enforce ?? 0) > 0 && (
              <span className={styles.splitEnforce} style={{ flexGrow: enforce ?? 0 }} />
            )}
            {splitTotal > 0 && (detect ?? 0) > 0 && (
              <span className={styles.splitDetect} style={{ flexGrow: detect ?? 0 }} />
            )}
          </div>
        </div>

        <div className={styles.heroReadouts}>
          <Readout
            label={t.overview.hero.p50}
            value={p50 ?? t.common.none}
            hint={latencyHint}
          />
          <Readout
            label={t.overview.hero.p95}
            value={p95 ?? t.common.none}
            hint={latencyHint}
          />
          <Readout
            label={t.overview.hero.passthrough}
            value={passthrough == null ? t.common.none : fmt(passthrough)}
            hint={passthroughHint}
            tone={passthrough != null && passthrough > 0 ? 'danger' : 'default'}
          />
          {base === 'audit' && replacements != null && (
            <Readout label={t.overview.hero.replacements} value={fmt(replacements)} />
          )}
        </div>
      </div>
    </Card>
  );
}

/** «Топ правил» as a plain HTML bar-list — long rule ids get CSS ellipsis, not axis clipping. */
function RuleBarList({ rules }: { rules: { name: string; value: number }[] }) {
  if (rules.length === 0) {
    return <span className={styles.muted}>{t.overview.section.noData}</span>;
  }
  const max = rules.reduce((a, r) => Math.max(a, r.value), 0);
  return (
    <div className={styles.ruleList}>
      {rules.map((r) => (
        <div key={r.name} className={styles.ruleRow}>
          <span className={styles.ruleId} title={r.name}>
            {r.name}
          </span>
          <span className={styles.ruleTrack}>
            <span
              className={styles.ruleFill}
              style={{ width: max > 0 ? `max(${(r.value / max) * 100}%, 3px)` : '0' }}
            />
          </span>
          <span className={styles.ruleCount}>{fmt(r.value)}</span>
        </div>
      ))}
    </div>
  );
}

function RecentRow({ record }: { record: AuditRecord }) {
  const replacements = record.replacements ?? [];
  return (
    <li className={styles.recentRow}>
      <span className={styles.recentTime}>{fmtTime(record.timestamp)}</span>
      <span className={styles.recentModel} title={record.model}>
        {record.model || t.common.none}
      </span>
      {record.mode === 'detect' && (
        <span className={styles.detectPill} title={t.status.modeDetectHint}>
          detect
        </span>
      )}
      <span className={styles.recentChips}>
        {replacements.length === 0 ? (
          <span className={styles.muted}>{t.overview.section.noReplacements}</span>
        ) : (
          replacements.map((rep, i) =>
            rep.placeholder ? (
              <MaskChip
                key={`${rep.placeholder}-${i}`}
                placeholder={rep.placeholder}
                original={rep.original}
                dataType={rep.data_type || undefined}
                size="s"
              />
            ) : null,
          )
        )}
      </span>
    </li>
  );
}

export function OverviewPage() {
  const audit = useRecentAudit(WINDOW);
  const metrics = useMetricsSummary();
  const dataTypesQuery = useDataTypes();

  const labels = useMemo(() => {
    const map = new Map<number, string>();
    for (const dt of dataTypesQuery.data ?? []) {
      map.set(Number(dt.data_type), dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)]);
    }
    return map;
  }, [dataTypesQuery.data]);
  const dtLabel = (id: number) => labels.get(id) ?? DATA_TYPE_NAME[id] ?? String(id);

  const agg: AuditAgg = useMemo(() => {
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

    // Entity order (color follows the entity, not the rank).
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

    return { total: records.length, enforce, detect, replacements, dataTypeData, topRules, overTime };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [audit.data, labels]);

  const recent = useMemo(() => {
    const records = [...(audit.data ?? [])];
    records.sort((a, b) => (b.timestamp ?? '').localeCompare(a.timestamp ?? ''));
    return records.slice(0, RECENT_COUNT);
  }, [audit.data]);

  const auditDisabled = isAuditDisabledError(audit.error);

  return (
    <div>
      <PageHeader
        title={t.overview.title}
        description={t.overview.description}
        actions={
          <ButtonOutline
            label={t.overview.refresh}
            icon={<UpdateSVG />}
            loading={audit.isFetching || metrics.isFetching}
            onClick={() => {
              audit.refetch();
              metrics.refetch();
            }}
          />
        }
      />

      <Hero metrics={metrics} agg={agg} auditAvailable={Boolean(audit.data)} />

      {auditDisabled ? (
        <Alert appearance="info" icon title={t.overview.disabledTitle} description={t.overview.disabledHint} />
      ) : (
        <QueryBoundary isLoading={audit.isLoading} error={audit.error} onRetry={() => audit.refetch()}>
          {agg.total === 0 ? (
            <Alert appearance="info" icon description={t.overview.empty} />
          ) : (
            <div className={styles.auditSections}>
              <div className={styles.sectionEyebrow}>{t.overview.section.auditWindow(WINDOW)}</div>

              <div className={styles.chartRow}>
                <Card title={t.overview.section.dataTypes}>
                  {agg.dataTypeData.length === 0 ? (
                    <span className={styles.muted}>{t.overview.section.noData}</span>
                  ) : (
                    <ResponsiveContainer
                      width="100%"
                      height={Math.max(agg.dataTypeData.length * 40 + 36, 120)}
                    >
                      <BarChart
                        layout="vertical"
                        data={agg.dataTypeData}
                        margin={{ top: 4, right: 16, bottom: 4, left: 0 }}
                      >
                        <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" horizontal={false} />
                        <XAxis
                          type="number"
                          allowDecimals={false}
                          tick={AXIS}
                          tickLine={false}
                          axisLine={{ stroke: 'var(--chart-grid)' }}
                        />
                        <YAxis
                          type="category"
                          dataKey="name"
                          width={140}
                          tick={AXIS}
                          tickLine={false}
                          axisLine={false}
                          tickFormatter={ellipsize}
                        />
                        <Tooltip
                          content={<ChartTooltip />}
                          cursor={{ fill: 'var(--sys-neutral-background-opacity)' }}
                        />
                        {/* No entrance animation: instrument panels report state, they don't perform. */}
                        <Bar
                          dataKey="value"
                          name={t.overview.chart.events}
                          barSize={12}
                          radius={[0, 4, 4, 0]}
                          isAnimationActive={false}
                        >
                          {agg.dataTypeData.map((d) => (
                            <Cell key={d.id} fill={CHART_COLOR[d.id] ?? 'var(--chart-dt-6)'} />
                          ))}
                        </Bar>
                      </BarChart>
                    </ResponsiveContainer>
                  )}
                </Card>

                <Card title={t.overview.chart.topRules}>
                  <RuleBarList rules={agg.topRules} />
                </Card>
              </div>

              <Card title={t.overview.section.dynamics}>
                <ResponsiveContainer width="100%" height={220}>
                  <AreaChart data={agg.overTime} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
                    <defs>
                      <linearGradient id="ov-area" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="0%" stopColor="var(--chart-primary)" stopOpacity={0.35} />
                        <stop offset="100%" stopColor="var(--chart-primary)" stopOpacity={0.02} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--chart-grid)" vertical={false} />
                    <XAxis
                      dataKey="time"
                      tick={AXIS}
                      tickLine={false}
                      axisLine={{ stroke: 'var(--chart-grid)' }}
                      minTickGap={24}
                    />
                    <YAxis allowDecimals={false} tick={AXIS} tickLine={false} axisLine={false} width={40} />
                    <Tooltip content={<ChartTooltip />} />
                    <Area
                      type="monotone"
                      dataKey="count"
                      name={t.overview.chart.events}
                      stroke="var(--chart-primary)"
                      strokeWidth={2}
                      fill="url(#ov-area)"
                      isAnimationActive={false}
                    />
                  </AreaChart>
                </ResponsiveContainer>
              </Card>

              <Card
                title={t.overview.section.recent}
                action={
                  <Link to="/audit" className={styles.allLink}>
                    {t.overview.section.fullAudit}
                  </Link>
                }
              >
                <ul className={styles.recentList}>
                  {recent.map((r, i) => (
                    <RecentRow key={r.request_id ?? i} record={r} />
                  ))}
                </ul>
              </Card>
            </div>
          )}
        </QueryBoundary>
      )}
    </div>
  );
}
