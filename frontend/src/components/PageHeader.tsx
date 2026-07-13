import { Typography } from '@snack-uikit/typography';
import type { ReactNode } from 'react';

import styles from './Page.module.scss';

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <div className={styles.header}>
      <div className={styles.headerText}>
        <Typography family="sans" purpose="headline" size="m" tag="h1">
          {title}
        </Typography>
        {description && (
          <Typography
            family="sans"
            purpose="body"
            size="m"
            tag="p"
            className={styles.headerDescription}
          >
            {description}
          </Typography>
        )}
      </div>
      {actions && <div className={styles.headerActions}>{actions}</div>}
    </div>
  );
}
