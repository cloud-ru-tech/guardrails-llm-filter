import styles from './StatTile.module.scss';

/**
 * An instrument readout: a small mono value under a sentence-case label.
 * `tone="danger"` is for alarm values (e.g. a nonzero passthrough count on a
 * fail-open proxy) — the reserved error red, never used for entities.
 */
export function Readout({
  label,
  value,
  hint,
  tone = 'default',
}: {
  label: string;
  value: string | number;
  hint?: string;
  tone?: 'default' | 'accent' | 'danger';
}) {
  return (
    <div className={styles.readout} title={hint}>
      <span
        className={
          tone === 'default' ? styles.readoutValue : `${styles.readoutValue} ${styles[tone]}`
        }
      >
        {value}
      </span>
      <span className={styles.readoutLabel}>{label}</span>
    </div>
  );
}

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
      <span className={`${styles.value} ${accent ? styles.accent : ''}`}>{value}</span>
      {hint && <span className={styles.hint}>{hint}</span>}
    </div>
  );
}
