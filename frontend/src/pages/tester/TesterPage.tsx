import { Alert } from '@snack-uikit/alert';
import { ButtonFilled } from '@snack-uikit/button';
import { FieldSelect, FieldTextArea } from '@snack-uikit/fields';
import { PlaySVG } from '@snack-uikit/icons';
import { Typography } from '@snack-uikit/typography';
import { useMemo, useState } from 'react';

import { ApiRequestError } from '@/api/client';
import { useDataTypes, useScan } from '@/api/hooks';
import { Card } from '@/components/Card';
import { PageHeader } from '@/components/PageHeader';
import { ScanResultPanel } from '@/components/ScanResultPanel';
import { DATA_TYPE_NAME } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './TesterPage.module.scss';

export function TesterPage() {
  const [text, setText] = useState('');
  const [dataTypes, setDataTypes] = useState<number[]>([]);

  const dataTypesQuery = useDataTypes();
  const scan = useScan();

  const dataTypeLabels = useMemo(() => {
    const map = new Map<number, string>();
    for (const dt of dataTypesQuery.data ?? []) {
      map.set(Number(dt.data_type), dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)]);
    }
    return map;
  }, [dataTypesQuery.data]);

  const dtLabel = (id: number) => dataTypeLabels.get(id) ?? DATA_TYPE_NAME[id] ?? String(id);

  const dataTypeOptions = useMemo(
    () =>
      (dataTypesQuery.data ?? []).map((dt) => ({
        value: Number(dt.data_type),
        option: dt.display_name || dt.name || DATA_TYPE_NAME[Number(dt.data_type)] || String(dt.data_type),
      })),
    [dataTypesQuery.data],
  );

  const handleScan = () => {
    scan.mutate({
      text,
      data_types: dataTypes.length ? dataTypes : undefined,
    });
  };

  const errorMessage =
    scan.error instanceof ApiRequestError
      ? scan.error.details
        ? `${scan.error.message} — ${scan.error.details}`
        : scan.error.message
      : scan.error
        ? t.tester.scanError
        : null;

  return (
    <div>
      <PageHeader title={t.tester.title} description={t.tester.description} />

      <div className={styles.grid}>
        <Card title={t.tester.input}>
          <div className={styles.form}>
            <FieldTextArea
              value={text}
              onChange={setText}
              placeholder={t.tester.inputPlaceholder}
              minRows={8}
            />
            <FieldSelect
              selection="multiple"
              label={t.tester.dataTypes}
              hint={t.tester.dataTypesHint}
              options={dataTypeOptions}
              value={dataTypes}
              onChange={(v) => setDataTypes((v ?? []).map(Number))}
              loading={dataTypesQuery.isLoading}
            />
            <div className={styles.actions}>
              <ButtonFilled
                label={t.tester.scan}
                icon={<PlaySVG />}
                onClick={handleScan}
                loading={scan.isPending}
                disabled={!text.trim()}
              />
            </div>
          </div>
        </Card>

        <Card title={t.tester.result.masked}>
          {errorMessage && <Alert appearance="error" icon description={errorMessage} />}
          {scan.data ? (
            <ScanResultPanel result={scan.data} dtLabel={dtLabel} />
          ) : (
            !errorMessage && (
              <Typography family="sans" purpose="body" size="s" tag="span" className={styles.muted}>
                {t.tester.empty}
              </Typography>
            )
          )}
        </Card>
      </div>
    </div>
  );
}
