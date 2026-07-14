import { ButtonFunction } from '@snack-uikit/button';
import { DrawerCustom } from '@snack-uikit/drawer';
import { CrossSVG } from '@snack-uikit/icons';
import { Typography } from '@snack-uikit/typography';

import type { AuditRecord } from '@/api/types';
import { EntityChip } from '@/components/EntityChip';
import { MaskChip, MaskedText } from '@/components/MaskedText';
import { t } from '@/i18n/strings';

import styles from './AuditDetailDrawer.module.scss';

function SectionTitle({ children }: { children: string }) {
  return (
    <Typography family="sans" purpose="label" size="s" tag="span" className={styles.sectionTitle}>
      {children}
    </Typography>
  );
}

export function AuditDetailDrawer({
  record,
  onClose,
  dtLabel,
}: {
  record: AuditRecord | null;
  onClose: () => void;
  dtLabel: (id: number) => string;
}) {
  // Map placeholder -> original for the "reveal on hover" feature. Populated
  // only when the backend stores originals (GUARDRAILS_AUDIT_STORE_ORIGINAL_TEXTS);
  // otherwise every `original` is absent and the map stays empty.
  const originals: Record<string, string> = {};
  // Map placeholder -> data type so mask-chips carry their entity tick.
  const chipDataTypes: Record<string, number> = {};
  for (const r of record?.replacements ?? []) {
    if (!r.placeholder) continue;
    if (r.original) originals[r.placeholder] = r.original;
    if (r.data_type) chipDataTypes[r.placeholder] = r.data_type;
  }

  return (
    <DrawerCustom open={Boolean(record)} onClose={onClose} size="min(560px, 100vw)">
      <div className={styles.drawer}>
        <div className={styles.header}>
          <Typography family="sans" purpose="title" size="s" tag="h2">
            {t.audit.detail.title}
          </Typography>
          <ButtonFunction icon={<CrossSVG />} aria-label={t.common.close} onClick={onClose} />
        </div>

        {record && (
          <div className={styles.body}>
            <div className={styles.section}>
              <div className={styles.kv}>
                <span className={styles.kvKey}>{t.audit.detail.requestId}</span>
                <span className={styles.mono}>{record.request_id}</span>
                <span className={styles.kvKey}>{t.audit.detail.timestamp}</span>
                <span className={styles.mono}>{record.timestamp}</span>
                <span className={styles.kvKey}>{t.audit.detail.mode}</span>
                <span>
                  {record.mode === 'detect' ? (
                    <span className={styles.detectPill} title={t.audit.detectHint}>
                      detect
                    </span>
                  ) : (
                    <span className={styles.mono}>{record.mode ?? t.common.none}</span>
                  )}
                </span>
                <span className={styles.kvKey}>{t.audit.detail.model}</span>
                <span>{record.model || t.common.none}</span>
                <span className={styles.kvKey}>{t.audit.detail.path}</span>
                <span className={styles.mono}>{record.path || t.common.none}</span>
              </div>
            </div>

            <div className={styles.section}>
              <SectionTitle>{t.audit.detail.triggeredDataTypes}</SectionTitle>
              <div className={styles.chips}>
                {(record.triggered_data_types ?? []).length === 0
                  ? t.common.none
                  : (record.triggered_data_types ?? []).map((id) => (
                      <EntityChip key={id} dataType={id} size="s">
                        {dtLabel(id)}
                      </EntityChip>
                    ))}
              </div>
            </div>

            <div className={styles.section}>
              <SectionTitle>{t.audit.detail.triggeredRules}</SectionTitle>
              <div className={styles.chips}>
                {(record.triggered_rule_ids ?? []).length === 0
                  ? t.common.none
                  : (record.triggered_rule_ids ?? []).map((id) => (
                      <span key={id} className={styles.ruleChip}>
                        {id}
                      </span>
                    ))}
              </div>
            </div>

            {(record.replacements ?? []).length > 0 && (
              <div className={styles.section}>
                <SectionTitle>{t.audit.detail.replacements}</SectionTitle>
                <div className={styles.tableWrap}>
                  <table className={styles.replTable}>
                    <thead>
                      <tr>
                        <th>{t.audit.detail.replRule}</th>
                        <th>{t.audit.detail.replDataType}</th>
                        <th>{t.audit.detail.replPlaceholder}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {(record.replacements ?? []).map((r, i) => (
                        <tr key={`${r.rule_id}-${r.placeholder}-${i}`}>
                          <td className={styles.mono}>{r.rule_id}</td>
                          <td>
                            {r.data_type ? (
                              <EntityChip dataType={r.data_type} size="s">
                                {dtLabel(r.data_type)}
                              </EntityChip>
                            ) : (
                              t.common.none
                            )}
                          </td>
                          <td>
                            {r.placeholder ? (
                              <MaskChip
                                placeholder={r.placeholder}
                                original={r.original}
                                dataType={r.data_type || undefined}
                                size="s"
                              />
                            ) : (
                              t.common.none
                            )}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}

            {(record.masked_texts ?? []).length > 0 && (
              <div className={styles.section}>
                <SectionTitle>{t.audit.detail.maskedRequest}</SectionTitle>
                {(record.masked_texts ?? []).map((text, i) => (
                  <MaskedText key={i} text={text} originals={originals} dataTypes={chipDataTypes} size="s" />
                ))}
              </div>
            )}

            {(record.masked_response_texts ?? []).length > 0 && (
              <div className={styles.section}>
                <SectionTitle>{t.audit.detail.maskedResponse}</SectionTitle>
                {(record.masked_response_texts ?? []).map((text, i) => (
                  <MaskedText key={i} text={text} originals={originals} dataTypes={chipDataTypes} size="s" />
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </DrawerCustom>
  );
}
