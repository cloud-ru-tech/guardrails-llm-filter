import type { ReactNode } from 'react';

import type { BadgeTone } from '@/domain/dataTypes';

import styles from './Badge.module.scss';

export function Badge({ tone = 'neutral', children }: { tone?: BadgeTone; children: ReactNode }) {
  return <span className={`${styles.badge} ${styles[tone]}`}>{children}</span>;
}
