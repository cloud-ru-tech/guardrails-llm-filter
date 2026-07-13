import { Typography } from '@snack-uikit/typography';

import type { ScanResponse } from '@/api/types';
import { Badge } from '@/components/Badge';
import { MaskedText } from '@/components/MaskedText';
import { DATA_TYPE_TONE } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './ScanResultPanel.module.scss';

function SectionTitle({ children }: { children: string }) {
  return (
    <Typography family="sans" purpose="label" size="s" tag="span" className={styles.sectionTitle}>
      {children}
    </Typography>
  );
}

export function ScanResultPanel({
  result,
  dtLabel,
}: {
  result: ScanResponse;
  dtLabel: (id: number) => string;
}) {
  const maskedTexts = result.masked_texts ?? [];
  const rules = result.triggered_rule_ids ?? [];
  const dataTypes = result.triggered_data_types ?? [];
  const placeholders = result.placeholders ?? [];
  const totalMs = result.timings?.total_ms;

  const noMatches = rules.length === 0 && dataTypes.length === 0 && placeholders.length === 0;

  return (
    <div className={styles.panel}>
      {maskedTexts.length > 0 && (
        <div className={styles.section}>
          <SectionTitle>{t.tester.result.masked}</SectionTitle>
          {maskedTexts.map((text, i) => (
            <MaskedText key={i} text={text} />
          ))}
        </div>
      )}

      {noMatches ? (
        <Typography family="sans" purpose="body" size="s" tag="span" className={styles.muted}>
          {t.tester.noMatches}
        </Typography>
      ) : (
        <>
          <div className={styles.section}>
            <SectionTitle>{t.tester.result.triggeredDataTypes}</SectionTitle>
            <div className={styles.chips}>
              {dataTypes.length === 0
                ? t.common.none
                : dataTypes.map((id) => (
                    <Badge key={id} tone={DATA_TYPE_TONE[id] ?? 'neutral'}>
                      {dtLabel(id)}
                    </Badge>
                  ))}
            </div>
          </div>

          <div className={styles.section}>
            <SectionTitle>{t.tester.result.triggeredRules}</SectionTitle>
            <div className={styles.chips}>
              {rules.length === 0
                ? t.common.none
                : rules.map((id) => (
                    <Badge key={id} tone="neutral">
                      {id}
                    </Badge>
                  ))}
            </div>
          </div>

          {placeholders.length > 0 && (
            <div className={styles.section}>
              <SectionTitle>{t.tester.result.placeholders}</SectionTitle>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>{t.tester.result.phPlaceholder}</th>
                    <th>{t.tester.result.phOriginal}</th>
                    <th>{t.tester.result.phRule}</th>
                  </tr>
                </thead>
                <tbody>
                  {placeholders.map((p, i) => (
                    <tr key={`${p.placeholder}-${i}`}>
                      <td className={styles.mono}>{p.placeholder}</td>
                      <td className={styles.mono}>{p.original}</td>
                      <td className={styles.mono}>{p.rule_id}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      {totalMs != null && (
        <Typography family="sans" purpose="body" size="s" tag="span" className={styles.muted}>
          {t.tester.result.timing(totalMs)}
        </Typography>
      )}
    </div>
  );
}
