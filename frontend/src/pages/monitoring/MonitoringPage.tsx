import { ButtonFilled, ButtonOutline } from '@snack-uikit/button';
import { Card } from '@snack-uikit/card';
import { CopySVG, EyeSVG, FileSVG, UpdateSVG } from '@snack-uikit/icons';
import { toaster } from '@snack-uikit/toaster';
import { Typography } from '@snack-uikit/typography';

import { useDataTypes, useHealth, useMetricsSummary, useVersion } from '@/api/hooks';
import type { Latency } from '@/api/types';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { Readout } from '@/components/StatTile';
import { CHART_COLOR, DATA_TYPE, DATA_TYPE_NAME } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './MonitoringPage.module.scss';

const SCRAPE_SNIPPET = `scrape_configs:
  - job_name: guardrails-llm-filter
    scrape_interval: 15s
    static_configs:
      # GUARDRAILS_METRICS_PORT, по умолчанию 9090
      - targets: ['guardrails-llm-filter:9090']`;

/** Adaptive latency formatting: sub-ms values must not read as a broken «0 ms». */
function fmtMs(seconds?: number, count?: number): string {
  if (!count) return '—';
  if (seconds == null) return '—';
  const ms = seconds * 1000;
  if (ms > 0 && ms < 1) return '<1 мс';
  return `${Math.round(ms)} мс`;
}

function CodeBlock({ code, copyLabel }: { code: string; copyLabel: string }) {
  const copy = async () => {
    try {
      if (navigator.clipboard) {
        await navigator.clipboard.writeText(code);
      } else {
        // The console commonly runs on plain HTTP (:9080) where the async
        // clipboard API is unavailable — fall back to the legacy path.
        const area = document.createElement('textarea');
        area.value = code;
        area.style.position = 'fixed';
        area.style.opacity = '0';
        document.body.appendChild(area);
        area.select();
        const ok = document.execCommand('copy');
        area.remove();
        if (!ok) throw new Error('execCommand copy failed');
      }
      toaster.userAction.success({ label: t.monitoring.prometheus.copied });
    } catch {
      toaster.userAction.error({ label: t.monitoring.prometheus.copyFailed });
    }
  };
  return (
    <div className={styles.codeBlock}>
      <pre className={styles.code}>{code}</pre>
      <ButtonOutline
        size="xs"
        appearance="neutral"
        icon={<CopySVG />}
        label={copyLabel}
        onClick={copy}
        className={styles.copyButton}
      />
    </div>
  );
}

/** Resolves a data-type label from the metrics summary back to its numeric id. */
function dataTypeIdByName(label: string): number | undefined {
  const entry = Object.entries(DATA_TYPE_NAME).find(([, name]) => name === label);
  return entry ? Number(entry[0]) : DATA_TYPE[label as keyof typeof DATA_TYPE];
}

type BarItem = { key: string; label: string; count: number; color?: string };

/**
 * The same visual language as the Overview's top-rules list: label, a thin
 * proportional bar, mono count. Entity rows carry their palette color; plain
 * rows use the accent.
 */
function BarList({ items, empty }: { items: BarItem[]; empty: string }) {
  if (items.length === 0) {
    return <span className={styles.muted}>{empty}</span>;
  }
  const max = Math.max(...items.map((i) => i.count), 1);
  return (
    <ul className={styles.barList}>
      {items.map((item) => (
        <li key={item.key} className={styles.barRow}>
          <span className={styles.barLabel} title={item.label}>
            {item.color && (
              <span
                className={styles.barDot}
                style={{ background: item.color }}
                aria-hidden="true"
              />
            )}
            {item.label}
          </span>
          <span className={styles.barTrack}>
            <span
              className={styles.barFill}
              style={{
                width: `${Math.max((item.count / max) * 100, 4)}%`,
                background: item.color ?? 'var(--chart-primary)',
              }}
            />
          </span>
          <span className={styles.barCount}>{item.count}</span>
        </li>
      ))}
    </ul>
  );
}

export function MonitoringPage() {
  const health = useHealth();
  const version = useVersion();
  const metrics = useMetricsSummary();
  const dataTypesQuery = useDataTypes();

  const dtLabel = (id: number): string => {
    const dt = (dataTypesQuery.data ?? []).find((d) => Number(d.data_type) === id);
    return dt?.display_name || dt?.name || DATA_TYPE_NAME[id] || String(id);
  };

  const m = metrics.data;
  // Three-state health: a still-pending first probe must not read as an outage.
  const healthState: 'pending' | 'ok' | 'down' = health.isPending
    ? 'pending'
    : !health.isError && Boolean(health.data)
      ? 'ok'
      : 'down';
  const mode = health.data?.mode ?? version.data?.mode;

  const maskedByMode = m?.requests_masked_total ?? {};
  const maskedTotal = Object.values(maskedByMode).reduce((a, b) => a + b, 0);
  const passthrough = m?.passthrough_total ?? {};
  const passthroughTotal = Object.values(passthrough).reduce((a, b) => a + b, 0);

  const latencyRows: { key: string; stage: string; latency?: Latency }[] = [
    {
      key: 'pipeline',
      stage: t.monitoring.counters.stagePipeline,
      latency: m?.latency_seconds?.pipeline,
    },
    { key: 'mask', stage: t.monitoring.counters.stageMask, latency: m?.latency_seconds?.mask },
    {
      key: 'demask',
      stage: t.monitoring.counters.stageDemask,
      latency: m?.latency_seconds?.demask,
    },
  ];

  const topRules: BarItem[] = [...(m?.rule_triggers_total ?? [])]
    .sort((a, b) => (b.count ?? 0) - (a.count ?? 0))
    .slice(0, 8)
    .map((r) => ({ key: r.label ?? '', label: r.label ?? '', count: r.count ?? 0 }));

  const topDataTypes: BarItem[] = [...(m?.data_type_triggers_total ?? [])]
    .sort((a, b) => (b.count ?? 0) - (a.count ?? 0))
    .map((d) => {
      const id = dataTypeIdByName(d.label ?? '');
      return {
        key: d.label ?? '',
        label: id != null ? dtLabel(id) : (d.label ?? ''),
        count: d.count ?? 0,
        color: id != null ? CHART_COLOR[id] : undefined,
      };
    });

  return (
    <div>
      <PageHeader
        title={t.monitoring.title}
        description={t.monitoring.description}
        actions={
          <ButtonFilled
            appearance="neutral"
            label={t.monitoring.refresh}
            icon={<UpdateSVG />}
            loading={metrics.isFetching}
            onClick={() => {
              metrics.refetch();
              health.refetch();
              // The status band shows version/commit/store — refresh them too,
              // otherwise a just-rolled-out build keeps its old version here.
              version.refetch();
            }}
          />
        }
      />

      <div className={styles.page}>
        {/* Status band: one glance — is the service alive and in what shape. */}
        <Card outline size="m" className={styles.card}>
          <div className={styles.statusBand}>
            <div className={styles.healthCluster}>
              <span
                className={`${styles.healthDot} ${
                  healthState === 'ok'
                    ? styles.healthDotOk
                    : healthState === 'down'
                      ? styles.healthDotBad
                      : styles.healthDotPending
                }`}
                aria-hidden="true"
              />
              <div className={styles.healthText}>
                <Typography family="sans" purpose="title" size="s" tag="div">
                  {healthState === 'ok'
                    ? t.status.healthy
                    : healthState === 'down'
                      ? t.status.unhealthy
                      : t.monitoring.status.checking}
                </Typography>
                <span className={styles.mutedSmall}>{t.monitoring.status.section}</span>
              </div>
            </div>
            <div className={styles.statusReadouts}>
              <Readout label={t.monitoring.status.mode} value={mode ?? '—'} />
              <Readout
                label={t.monitoring.status.store}
                value={version.data?.store_backend ?? health.data?.store_backend ?? '—'}
              />
              <Readout label={t.monitoring.status.topology} value={version.data?.topology ?? '—'} />
              <Readout label={t.monitoring.status.version} value={version.data?.version ?? '—'} />
              <Readout label={t.monitoring.status.commit} value={version.data?.commit ?? '—'} />
            </div>
          </div>
        </Card>

        <QueryBoundary
          isLoading={metrics.isLoading}
          error={metrics.error}
          onRetry={() => metrics.refetch()}
        >
          <span className={styles.eyebrow}>{t.monitoring.counters.section}</span>
          <div className={styles.grid}>
            <Card
              outline
              size="m"
              className={styles.card}
              header={
                <Card.Header
                  title={t.monitoring.counters.masked}
                  description={t.monitoring.counters.sectionHint}
                />
              }
            >
              <div className={styles.trafficBody}>
                <span className={styles.heroValue}>{maskedTotal}</span>
                <div className={styles.readouts}>
                  <Readout
                    label={t.monitoring.counters.enforce}
                    value={maskedByMode.enforce ?? 0}
                  />
                  <Readout label={t.monitoring.counters.detect} value={maskedByMode.detect ?? 0} />
                  <Readout
                    label={t.monitoring.counters.passthrough}
                    value={passthroughTotal}
                    tone={passthroughTotal > 0 ? 'danger' : 'default'}
                    hint={t.monitoring.counters.passthroughHint}
                  />
                </div>
                {passthroughTotal > 0 && (
                  <div className={styles.readouts}>
                    <Readout
                      label={t.monitoring.counters.ptUnguardedPath}
                      value={passthrough.unguarded_path ?? 0}
                    />
                    <Readout
                      label={t.monitoring.counters.ptUnknownFormat}
                      value={passthrough.unknown_format ?? 0}
                    />
                    <Readout
                      label={t.monitoring.counters.ptUnsupportedSchema}
                      value={passthrough.unsupported_schema ?? 0}
                    />
                  </div>
                )}
              </div>
            </Card>

            <Card
              outline
              size="m"
              className={styles.card}
              header={
                <Card.Header
                  title={t.monitoring.counters.latency}
                  description={t.monitoring.counters.latencyHint}
                />
              }
            >
              <table className={styles.latencyTable}>
                <thead>
                  <tr>
                    <th>{t.monitoring.counters.latencyStage}</th>
                    <th className={styles.num}>{t.monitoring.counters.latencyCount}</th>
                    <th className={styles.num}>p50</th>
                    <th className={styles.num}>p95</th>
                  </tr>
                </thead>
                <tbody>
                  {latencyRows.map((row) => (
                    <tr key={row.key}>
                      <td className={styles.mono}>{row.stage}</td>
                      <td className={`${styles.mono} ${styles.num}`}>{row.latency?.count ?? 0}</td>
                      <td className={`${styles.mono} ${styles.num}`}>
                        {fmtMs(row.latency?.p50, row.latency?.count)}
                      </td>
                      <td className={`${styles.mono} ${styles.num}`}>
                        {fmtMs(row.latency?.p95, row.latency?.count)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>

            <Card
              outline
              size="m"
              className={styles.card}
              header={<Card.Header title={t.monitoring.counters.topRules} />}
            >
              <BarList items={topRules} empty={t.monitoring.counters.empty} />
            </Card>

            <Card
              outline
              size="m"
              className={styles.card}
              header={<Card.Header title={t.monitoring.counters.topDataTypes} />}
            >
              <BarList items={topDataTypes} empty={t.monitoring.counters.empty} />
            </Card>
          </div>
        </QueryBoundary>

        <span className={styles.eyebrow}>{t.monitoring.hookupSection}</span>
        <div className={styles.grid}>
          <Card
            outline
            size="m"
            className={styles.card}
            header={
              <Card.Header
                title={t.monitoring.prometheus.section}
                description={t.monitoring.prometheus.sectionHint}
                truncate={{ description: 4 }}
                emblem={{ icon: EyeSVG, decor: true, appearance: 'primary', shape: 'square' }}
              />
            }
          >
            <div className={styles.cardStack}>
              <div>
                <Typography family="sans" purpose="label" size="m" tag="div">
                  {t.monitoring.prometheus.scrapeTitle}
                </Typography>
                <CodeBlock code={SCRAPE_SNIPPET} copyLabel={t.monitoring.prometheus.copy} />
              </div>

              <div>
                <Typography family="sans" purpose="label" size="m" tag="div">
                  {t.monitoring.prometheus.alertsTitle}
                </Typography>
                <Typography
                  family="sans"
                  purpose="body"
                  size="s"
                  tag="div"
                  className={styles.muted}
                >
                  {t.monitoring.prometheus.alertsHint}
                </Typography>
                <ul className={styles.pathList}>
                  <li>
                    <code className={styles.inlineCode}>
                      deploy/prometheus/guardrails-llm-filter-alerts.yml
                    </code>
                  </li>
                  <li>
                    <span className={styles.muted}>{t.monitoring.prometheus.alertsK8s} </span>
                    <code className={styles.inlineCode}>
                      deploy/kubernetes/components/monitoring/
                    </code>
                  </li>
                </ul>
                <Typography
                  family="sans"
                  purpose="body"
                  size="s"
                  tag="div"
                  className={styles.muted}
                >
                  {t.monitoring.prometheus.metricsDoc}
                </Typography>
              </div>
            </div>
          </Card>

          <Card
            outline
            size="m"
            className={styles.card}
            header={
              <Card.Header
                title={t.monitoring.grafana.section}
                description={t.monitoring.grafana.sectionHint}
                truncate={{ description: 4 }}
                emblem={{ icon: FileSVG, decor: true, appearance: 'primary', shape: 'square' }}
              />
            }
          >
            <div>
              <Typography family="sans" purpose="label" size="m" tag="div">
                {t.monitoring.grafana.importTitle}
              </Typography>
              <ol className={styles.stepList}>
                <li>{t.monitoring.grafana.step1}</li>
                <li>{t.monitoring.grafana.step2}</li>
                <li>
                  {t.monitoring.grafana.step3}{' '}
                  <code className={styles.inlineCode}>deploy/grafana/dashboard.json</code>
                </li>
                <li>{t.monitoring.grafana.step4}</li>
              </ol>
              <Typography family="sans" purpose="body" size="s" tag="div" className={styles.muted}>
                {t.monitoring.grafana.docHint}
              </Typography>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
