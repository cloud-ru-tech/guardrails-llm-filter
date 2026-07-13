import { Typography } from '@snack-uikit/typography';

import styles from './StatTile.module.scss';

export function StatTile({
  label,
  value,
  hint,
  accent = false,
}: {
  label: string;
  value: string | number;
  hint?: string;
  accent?: boolean;
}) {
  return (
    <div className={styles.tile}>
      <span className={styles.label}>{label}</span>
      <Typography
        family="sans"
        purpose="display"
        size="s"
        tag="span"
        className={`${styles.value} ${accent ? styles.accent : ''}`}
      >
        {value}
      </Typography>
      {hint && (
        <Typography family="sans" purpose="body" size="s" tag="span" className={styles.hint}>
          {hint}
        </Typography>
      )}
    </div>
  );
}
