import { Alert } from '@snack-uikit/alert';
import { ButtonFilled } from '@snack-uikit/button';
import { FieldSelect } from '@snack-uikit/fields';
import { SegmentedControl } from '@snack-uikit/segmented-control';
import { toaster } from '@snack-uikit/toaster';
import { Switch } from '@snack-uikit/toggles';
import { Typography } from '@snack-uikit/typography';
import { useEffect, useMemo, useState } from 'react';

import { ApiRequestError } from '@/api/client';
import { useDataTypes, useSettings, useUpdateSettings } from '@/api/hooks';
import type { Settings } from '@/api/types';
import { PageHeader } from '@/components/PageHeader';
import { QueryBoundary } from '@/components/QueryBoundary';
import { DATA_TYPE_NAME } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './SettingsPage.module.scss';

type Mode = 'enforce' | 'detect';

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
    setMode(s.mode === 'detect' ? 'detect' : 'enforce');
    setDataTypes((s.data_types ?? []).map(Number));
  }, [settingsQuery.data]);

  const options = useMemo(
    () =>
      (dataTypesQuery.data ?? []).map((dt) => ({
        value: Number(dt.data_type),
        option: dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)] || String(dt.data_type),
        description: dt.description,
      })),
    [dataTypesQuery.data],
  );

  const handleSave = () => {
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
        <div className={styles.card}>
          <div className={styles.rowInline}>
            <Switch checked={enabled} onChange={setEnabled} />
            <div className={styles.row}>
              <Typography family="sans" purpose="label" size="m" tag="span">
                {t.settings.enabled}
              </Typography>
              <Typography
                family="sans"
                purpose="body"
                size="s"
                tag="span"
                className={styles.hint}
              >
                {t.settings.enabledHint}
              </Typography>
            </div>
          </div>

          <div className={styles.row}>
            <Typography family="sans" purpose="label" size="m" tag="span">
              {t.settings.mode}
            </Typography>
            <SegmentedControl<Mode>
              value={mode}
              onChange={setMode}
              items={[
                { value: 'enforce', label: t.settings.modeEnforce },
                { value: 'detect', label: t.settings.modeDetect },
              ]}
            />
            <Typography family="sans" purpose="body" size="s" tag="span" className={styles.hint}>
              {t.settings.modeEnforceHint}
            </Typography>
          </div>

          <div className={styles.row}>
            <FieldSelect
              selection="multiple"
              label={t.settings.dataTypes}
              hint={t.settings.dataTypesHint}
              value={dataTypes}
              onChange={(value) => setDataTypes((value ?? []).map(Number))}
              options={options}
              loading={dataTypesQuery.isLoading}
            />
          </div>

          <Alert
            appearance="info"
            icon
            description={t.settings.modeResetWarning}
          />

          <div className={styles.footer}>
            <ButtonFilled
              label={t.common.save}
              onClick={handleSave}
              loading={updateSettings.isPending}
            />
          </div>
        </div>
      </QueryBoundary>
    </div>
  );
}
