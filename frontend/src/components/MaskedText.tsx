import { Fragment } from 'react';

import styles from './MaskedText.module.scss';

// Placeholders look like `<EMAIL_1>` / `<PLACEHOLDER_2>`: an angle-bracketed
// token with no nested brackets. Splitting on the capturing group keeps the
// delimiters so we can wrap them.
const PLACEHOLDER_SPLIT = /(<[^<>\s]+>)/g;
const PLACEHOLDER_ONE = /^<[^<>\s]+>$/;

/**
 * Renders masked text in a monospace block with `<...>` placeholders
 * highlighted. When `originals` maps a placeholder to its raw value (only when
 * the backend is configured to store originals), that placeholder reveals the
 * original on hover via a native tooltip.
 */
export function MaskedText({
  text,
  originals,
}: {
  text: string;
  originals?: Record<string, string>;
}) {
  const parts = text.split(PLACEHOLDER_SPLIT);
  return (
    <div className={styles.masked}>
      {parts.map((part, i) => {
        if (!PLACEHOLDER_ONE.test(part)) {
          return <Fragment key={i}>{part}</Fragment>;
        }
        const original = originals?.[part];
        return (
          <mark
            key={i}
            className={original ? `${styles.placeholder} ${styles.revealable}` : styles.placeholder}
            title={original}
          >
            {part}
          </mark>
        );
      })}
    </div>
  );
}
