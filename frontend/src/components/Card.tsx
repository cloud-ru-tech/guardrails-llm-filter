import { Typography } from '@snack-uikit/typography';
import type { ReactNode } from 'react';

import styles from './Card.module.scss';

export function Card({
  title,
  subtitle,
  action,
  className,
  children,
}: {
  title?: string;
  subtitle?: string;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className ? `${styles.card} ${className}` : styles.card}>
      {(title || action) && (
        <div className={styles.header}>
          <div>
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
