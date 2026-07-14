import { useMemo } from 'react';

import { useRules } from '@/api/hooks';
import type { ScanResponse } from '@/api/types';
import { EntityChip } from '@/components/EntityChip';
import { MaskChip, MaskedText } from '@/components/MaskedText';
import { t } from '@/i18n/strings';

import styles from './ScanResultPanel.module.scss';

/** The mono-eyebrow role: the system's single ALL-CAPS label. */
function SectionTitle({ children }: { children: string }) {
  return <span className={styles.sectionTitle}>{children}</span>;
}

export function ScanResultPanel({
  result,
  dtLabel,
}: {
  result: ScanResponse;
  dtLabel: (id: number) => string;
}) {
  // Resolves rule_id → data_type so mask-chips get their entity tick. The
  // rules list is a cached query; unknown ids (e.g. a draft rule in the rule
  // form tester) simply render without a tick.
  const rulesQuery = useRules('all');

  const maskedTexts = result.masked_texts ?? [];
  const rules = result.triggered_rule_ids ?? [];
  const dataTypes = result.triggered_data_types ?? [];
  const placeholders = result.placeholders ?? [];
  const totalMs = result.timings?.total_ms;

  const dataTypeByRule = useMemo(() => {
    const map = new Map<string, number>();
    for (const rule of rulesQuery.data ?? []) {
      map.set(rule.rule_id, rule.data_type);
    }
    return map;
  }, [rulesQuery.data]);

  // placeholder → original / data type maps for MaskedText chips.
  const { originals, placeholderDataTypes } = useMemo(() => {
    const orig: Record<string, string> = {};
    const pdt: Record<string, number> = {};
    for (const p of result.placeholders ?? []) {
      if (!p.placeholder) continue;
      if (p.original) orig[p.placeholder] = p.original;
      const dt = p.rule_id != null ? dataTypeByRule.get(p.rule_id) : undefined;
      if (dt != null) pdt[p.placeholder] = dt;
    }
    return { originals: orig, placeholderDataTypes: pdt };
  }, [result.placeholders, dataTypeByRule]);

  const noMatches = rules.length === 0 && dataTypes.length === 0 && placeholders.length === 0;

  // Zero matches: a neutral banner plus the text as-is — proof of a clean pass.
  if (noMatches) {
    return (
      <div className={styles.panel}>
        <div className={styles.banner} role="status">
          <span className={styles.bannerCount}>{t.tester.result.noMatchesCount}</span>
          <span className={styles.bannerText}>— {t.tester.result.noMatchesText}</span>
        </div>
        {maskedTexts.map((text, i) => (
          <MaskedText key={i} text={text} />
        ))}
        {totalMs != null && <span className={styles.timing}>{t.tester.result.timing(totalMs)}</span>}
      </div>
    );
  }

  return (
    <div className={styles.panel}>
      {maskedTexts.length > 0 && (
        <div className={styles.section}>
          <SectionTitle>{t.tester.result.masked}</SectionTitle>
          {maskedTexts.map((text, i) => (
            <MaskedText key={i} text={text} originals={originals} dataTypes={placeholderDataTypes} />
          ))}
        </div>
      )}

      <div className={styles.section}>
        <SectionTitle>{t.tester.result.triggeredDataTypes}</SectionTitle>
        <div className={styles.chips}>
          {dataTypes.length === 0
            ? t.common.none
            : dataTypes.map((id) => (
                <EntityChip key={id} dataType={id}>
                  {dtLabel(id)}
                </EntityChip>
              ))}
        </div>
      </div>

      <div className={styles.section}>
        <SectionTitle>{t.tester.result.triggeredRules}</SectionTitle>
        <div className={styles.chips}>
          {rules.length === 0
            ? t.common.none
            : rules.map((id) => (
                <span key={id} className={styles.ruleChip} title={id}>
                  {id}
                </span>
              ))}
        </div>
      </div>

      {placeholders.length > 0 && (
        <div className={styles.section}>
          <SectionTitle>{t.tester.result.placeholders}</SectionTitle>
          <div className={styles.tableWrap}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th className={styles.colPlaceholder}>{t.tester.result.phPlaceholder}</th>
                  <th>{t.tester.result.phOriginal}</th>
                  <th className={styles.colRule}>{t.tester.result.phRule}</th>
                </tr>
              </thead>
              <tbody>
                {placeholders.map((p, i) => (
                  <tr key={`${p.placeholder}-${i}`}>
                    <td className={styles.cell}>
                      {p.placeholder ? (
                        <MaskChip
                          placeholder={p.placeholder}
                          dataType={p.rule_id != null ? dataTypeByRule.get(p.rule_id) : undefined}
                          size="s"
                        />
                      ) : (
                        '—'
                      )}
                    </td>
                    <td className={styles.cell} title={p.original}>
                      {p.original ?? '—'}
                    </td>
                    <td className={styles.cell} title={p.rule_id}>
                      {p.rule_id ?? '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {totalMs != null && <span className={styles.timing}>{t.tester.result.timing(totalMs)}</span>}
    </div>
  );
}
