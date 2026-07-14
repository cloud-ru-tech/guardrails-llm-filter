import { Fragment } from 'react';

import { CHART_COLOR } from '@/domain/dataTypes';

import styles from './MaskedText.module.scss';

// Placeholders look like `<EMAIL_1>` / `<PLACEHOLDER_2>`: an angle-bracketed
// token with no nested brackets. Splitting on the capturing group keeps the
// delimiters so we can wrap them.
const PLACEHOLDER_SPLIT = /(<[^<>\s]+>)/g;
const PLACEHOLDER_ONE = /^<[^<>\s]+>$/;

/**
 * The mask-chip: the product's native artifact (a masked span) as a
 * first-class visual atom. Mono, brackets muted, an optional 2px entity tick
 * tying the chip to the data-type palette, hover-reveal when the original
 * value is known.
 */
export function MaskChip({
  placeholder,
  original,
  dataType,
  size = 'm',
}: {
  placeholder: string;
  original?: string;
  dataType?: number;
  size?: 's' | 'm';
}) {
  const inner = placeholder.replace(/^<|>$/g, '');
  const tick = dataType != null ? CHART_COLOR[dataType] : undefined;
  return (
    <mark
      className={[
        styles.placeholder,
        size === 's' ? styles.sizeS : '',
        original ? styles.revealable : '',
      ]
        .filter(Boolean)
        .join(' ')}
      title={original}
    >
      {tick && <span className={styles.tick} style={{ background: tick }} aria-hidden="true" />}
      <span className={styles.bracket}>&lt;</span>
      {inner}
      <span className={styles.bracket}>&gt;</span>
    </mark>
  );
}

/**
 * Renders masked text in a monospace block with `<...>` placeholders rendered
 * as mask-chips. `originals` maps a placeholder to its raw value (only when
 * the backend stores originals) — those chips reveal the original on hover.
 * `dataTypes` maps a placeholder to its data-type id and paints the entity tick.
 */
export function MaskedText({
  text,
  originals,
  dataTypes,
  size = 'm',
}: {
  text: string;
  originals?: Record<string, string>;
  dataTypes?: Record<string, number>;
  size?: 's' | 'm';
}) {
  const parts = text.split(PLACEHOLDER_SPLIT);
  return (
    <div className={size === 's' ? `${styles.masked} ${styles.maskedS}` : styles.masked}>
      {parts.map((part, i) => {
        if (!PLACEHOLDER_ONE.test(part)) {
          return <Fragment key={i}>{part}</Fragment>;
        }
        return (
          <MaskChip
            key={i}
            placeholder={part}
            original={originals?.[part]}
            dataType={dataTypes?.[part]}
            size={size}
          />
        );
      })}
    </div>
  );
}
