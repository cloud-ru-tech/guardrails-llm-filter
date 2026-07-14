import { Typography } from '@snack-uikit/typography';
import type { ReactNode } from 'react';

import styles from './Card.module.scss';

export function Card({
  eyebrow,
  title,
  subtitle,
  action,
  className,
  children,
}: {
  /** The single ALL-CAPS role in the system: an 11px mono context label. */
  eyebrow?: string;
  title?: string;
  subtitle?: string;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `${styles.card} ${className}` : styles.card}>
      {(eyebrow || title || action) && (
        <div className={styles.header}>
          <div className={styles.headerText}>
            {eyebrow && <span className={styles.eyebrow}>{eyebrow}</span>}
            {title && (
              <Typography family="sans" purpose="title" size="s" tag="h2">
                {title}
              </Typography>
            )}
            {subtitle && (
              <Typography
                family="sans"
                purpose="body"
                size="s"
                tag="div"
                className={styles.subtitle}
              >
                {subtitle}
              </Typography>
            )}
          </div>
          {action}
        </div>
      )}
      {children}
    </section>
  );
}
