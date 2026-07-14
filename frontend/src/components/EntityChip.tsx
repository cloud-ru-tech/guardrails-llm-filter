import type { ReactNode } from 'react';

import { CHART_COLOR } from '@/domain/dataTypes';

import styles from './EntityChip.module.scss';

/**
 * A data-type entity mark: colored dot + text label. The dot color follows the
 * entity across every view (chart bar = table chip = drawer chip); the label is
 * mandatory — the palette's adjacent-pair CVD margin assumes text is always
 * present beside the color.
 */
export function EntityChip({
  dataType,
  children,
  size = 'm',
}: {
  dataType: number;
  children: ReactNode;
  size?: 's' | 'm';
}) {
  return (
    <span className={size === 's' ? `${styles.chip} ${styles.sizeS}` : styles.chip}>
      <span
        className={styles.dot}
        style={{ background: CHART_COLOR[dataType] ?? 'var(--sys-neutral-text-support)' }}
        aria-hidden="true"
      />
      {children}
    </span>
  );
}
