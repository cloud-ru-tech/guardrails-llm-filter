import { ButtonFilled, ButtonOutline } from '@snack-uikit/button';
import { Card } from '@snack-uikit/card';
import { Divider } from '@snack-uikit/divider';
import { FieldSelect } from '@snack-uikit/fields';
import { EyeClosedSVG, FilterSVG, FunctionSettingsSVG } from '@snack-uikit/icons';
import { toaster } from '@snack-uikit/toaster';
import { Radio, Switch } from '@snack-uikit/toggles';
import { Typography } from '@snack-uikit/typography';
import { useEffect, useMemo, useState } from 'react';

import { ApiRequestError } from '@/api/client';
import { useDataTypes, useSettings, useUpdateSettings } from '@/api/hooks';
import type { Settings } from '@/api/types';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { CHART_COLOR, DATA_TYPE_NAME } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './SettingsPage.module.scss';

type Mode = 'enforce' | 'detect';

function normalizeMode(mode: string | undefined | null): Mode {
  return mode === 'detect' ? 'detect' : 'enforce';
}

/** Order-insensitive fingerprint of a data-type selection for dirty checking. */
function typesKey(types: number[]): string {
  return [...types].sort((a, b) => a - b).join(',');
}

/**
 * One mode option: a snack-uikit Card as the visual tile with a REAL Radio
 * input inside a full-bleed label. The native radio group supplies the
 * click target, keyboard model and screen-reader semantics (snack Card is a
 * role-less div, so ARIA attributes on it are ignored); the Card's `checked`
 * prop only drives the DS selected styling.
 */
function ModeTile({
  checked,
  value,
  title,
  description,
  consequence,
  onSelect,
}: {
  checked: boolean;
  value: Mode;
  title: string;
  description: string;
  /** Amber protection-consequence line (detect only). */
  consequence?: string;
  onSelect: () => void;
}) {
  return (
    <Card outline size="s" checked={checked} className={styles.modeCard}>
      <label className={styles.modeTileLabel}>
        <Radio
          checked={checked}
          name="guardrails-mode"
          value={value}
          onChange={(next) => {
            if (next) onSelect();
          }}
        />
        <span className={styles.modeTileText}>
          <Typography family="sans" purpose="label" size="m" tag="span">
            {title}
          </Typography>
          <Typography family="sans" purpose="body" size="s" tag="span" className={styles.hint}>
            {description}
          </Typography>
          {consequence && (
            <Typography
              family="sans"
              purpose="body"
              size="s"
              tag="span"
              className={styles.modeTileConsequence}
            >
              {consequence}
            </Typography>
          )}
        </span>
      </label>
    </Card>
  );
}

export function SettingsPage() {
  const settingsQuery = useSettings();
  const dataTypesQuery = useDataTypes();
  const updateSettings = useUpdateSettings();

  const [enabled, setEnabled] = useState(true);
  const [mode, setMode] = useState<Mode>('enforce');
  const [dataTypes, setDataTypes] = useState<number[]>([]);

  // Seed local form state from the server settings once loaded.
  useEffect(() => {
    const s = settingsQuery.data;
    if (!s) return;
    setEnabled(Boolean(s.enabled));
    setMode(normalizeMode(s.mode));
    setDataTypes((s.data_types ?? []).map(Number));
  }, [settingsQuery.data]);

  const options = useMemo(
    () =>
      (dataTypesQuery.data ?? []).map((dt) => {
        const id = Number(dt.data_type);
        return {
          value: id,
          option: dt.display_name || dt.name || DATA_TYPE_NAME[id] || String(dt.data_type),
          description: dt.description,
          // Entity dot in the droplist row; the option text is its mandatory label.
          beforeContent: (
            <span
              className={styles.optionDot}
              style={{ background: CHART_COLOR[id] ?? 'var(--sys-neutral-text-support)' }}
              aria-hidden="true"
            />
          ),
        };
      }),
    [dataTypesQuery.data],
  );

  // Save stays disabled until the form actually diverges from the loaded settings.
  const dirty = useMemo(() => {
    const s = settingsQuery.data;
    if (!s) return false;
    return (
      Boolean(s.enabled) !== enabled ||
      normalizeMode(s.mode) !== mode ||
      typesKey((s.data_types ?? []).map(Number)) !== typesKey(dataTypes)
    );
  }, [settingsQuery.data, enabled, mode, dataTypes]);

  // «Отмена» rolls the form back to the last loaded server state.
  const handleReset = () => {
    const s = settingsQuery.data;
    if (!s) return;
    setEnabled(Boolean(s.enabled));
    setMode(normalizeMode(s.mode));
    setDataTypes((s.data_types ?? []).map(Number));
  };

  const handleSave = () => {
    const saved = settingsQuery.data;
    if (saved) {
      // Confirm ONLY when the pending change reduces protection.
      const turningOff = Boolean(saved.enabled) && !enabled;
      const droppingToDetect = normalizeMode(saved.mode) === 'enforce' && mode === 'detect';
      if (turningOff || droppingToDetect) {
        const message = turningOff ? t.settings.confirmDisable : t.settings.confirmDetect;
        if (!window.confirm(message)) return;
      }
    }
    const body: Settings = { enabled, data_types: dataTypes, mode };
    updateSettings.mutate(body, {
      onSuccess: () => toaster.userAction.success({ label: t.settings.saved }),
      onError: (err) =>
        toaster.userAction.error({
          label:
            err instanceof ApiRequestError && err.details
              ? `${t.settings.saveError}: ${err.details}`
              : t.settings.saveError,
        }),
    });
  };

  return (
    <div>
      <PageHeader title={t.settings.title} description={t.settings.description} />
      <QueryBoundary
        isLoading={settingsQuery.isLoading}
        error={settingsQuery.error}
        onRetry={() => settingsQuery.refetch()}
      >
        <form
          className={styles.form}
          onSubmit={(e) => {
            e.preventDefault();
            handleSave();
          }}
        >
          <Card
            outline
            size="m"
            className={styles.sectionCard}
            header={
              <Card.Header
                title={t.settings.sectionMasking}
                description={t.settings.enabledHint}
                emblem={{ icon: EyeClosedSVG, decor: true, appearance: 'primary', shape: 'square' }}
              />
            }
          >
            <label className={styles.switchRow}>
              <Switch checked={enabled} onChange={setEnabled} />
              <Typography family="sans" purpose="label" size="m" tag="span">
                {t.settings.enabled}
              </Typography>
            </label>
          </Card>

          <Card
            outline
            size="m"
            className={styles.sectionCard}
            header={
              <Card.Header
                title={t.settings.mode}
                description={t.settings.modeEnforceHint}
                emblem={{
                  icon: FunctionSettingsSVG,
                  decor: true,
                  appearance: 'primary',
                  shape: 'square',
                }}
              />
            }
          >
            <div className={styles.modeTiles} role="radiogroup" aria-label={t.settings.mode}>
              <ModeTile
                checked={mode === 'enforce'}
                value="enforce"
                title={t.settings.modeEnforce}
                description={t.settings.modeEnforceDesc}
                onSelect={() => setMode('enforce')}
              />
              <ModeTile
                checked={mode === 'detect'}
                value="detect"
                title={t.settings.modeDetect}
                description={t.settings.modeDetectDesc}
                consequence={t.settings.modeDetectConsequence}
                onSelect={() => setMode('detect')}
              />
            </div>
          </Card>

          <Card
            outline
            size="m"
            className={styles.sectionCard}
            header={
              <Card.Header
                title={t.settings.dataTypes}
                description={t.settings.dataTypesHint}
                emblem={{ icon: FilterSVG, decor: true, appearance: 'primary', shape: 'square' }}
              />
            }
          >
            <FieldSelect
              selection="multiple"
              value={dataTypes}
              onChange={(value) => setDataTypes((value ?? []).map(Number))}
              options={options}
              loading={dataTypesQuery.isLoading}
            />
          </Card>

          <Divider weight="light" />

          <div className={styles.formFooter}>
            <ButtonFilled
              type="submit"
              label={t.common.save}
              disabled={!dirty}
              loading={updateSettings.isPending}
            />
            <ButtonOutline
              type="button"
              appearance="neutral"
              label={t.common.cancel}
              disabled={!dirty || updateSettings.isPending}
              onClick={handleReset}
            />
            {dirty && (
              <Typography
                family="sans"
                purpose="body"
                size="s"
                tag="span"
                className={styles.hint}
              >
                {t.settings.unsavedChanges}
              </Typography>
            )}
          </div>

          {/* PUT replaces the WHOLE settings document — a concurrent editor's
              save silently overwrites yours, so the caveat stays visible. */}
          <Typography family="sans" purpose="body" size="s" tag="div" className={styles.hint}>
            {t.settings.modeResetWarning}
          </Typography>
        </form>
      </QueryBoundary>
    </div>
  );
}
