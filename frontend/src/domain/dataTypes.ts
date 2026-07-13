// Mirrors internal/models/data_type.go — the numeric IDs are a wire contract and
// must stay consistent with the Go enum, the YAML rule files and the API.

export const DATA_TYPE = {
  CREDENTIALS: 1,
  API_KEYS: 2,
  ACCESS_TOKENS: 3,
  IP_ADDRESSES: 4,
  PERSONAL_DATA: 5,
  CUSTOM: 6,
} as const;

export type DataTypeId = (typeof DATA_TYPE)[keyof typeof DATA_TYPE];

/** Fallback English names; the API's /v1/data-types provides localized display names. */
export const DATA_TYPE_NAME: Record<number, string> = {
  1: 'CREDENTIALS',
  2: 'API_KEYS',
  3: 'ACCESS_TOKENS',
  4: 'IP_ADDRESSES',
  5: 'PERSONAL_DATA',
  6: 'CUSTOM',
};

/** Badge appearance per data type (snack StatusIndicator/Badge palette). */
export type BadgeTone = 'red' | 'orange' | 'yellow' | 'blue' | 'violet' | 'green' | 'neutral';

export const DATA_TYPE_TONE: Record<number, BadgeTone> = {
  1: 'red',
  2: 'orange',
  3: 'yellow',
  4: 'blue',
  5: 'violet',
  6: 'neutral',
};

/** Chart mark color per data type — resolves to theme-aware CSS vars from theme-overrides.scss. */
export const CHART_COLOR: Record<number, string> = {
  1: 'var(--chart-dt-1)',
  2: 'var(--chart-dt-2)',
  3: 'var(--chart-dt-3)',
  4: 'var(--chart-dt-4)',
  5: 'var(--chart-dt-5)',
  6: 'var(--chart-dt-6)',
};

/** Display order for data types in charts/legends. */
export const DATA_TYPE_ORDER = [1, 2, 3, 4, 5, 6] as const;

/** Validators accepted by the API (openapi Rule.validators enum). */
export const VALIDATORS = [
  'luhn',
  'snils',
  'inn_person',
  'inn_org',
  'ogrn',
  'ogrnip',
  'iban_mod97',
  'email_ascii',
  'payment_card',
  'entropy',
  'banlist',
  'ip_v4',
  'ip_v6',
  'ip_public',
  'ip_private',
] as const;

export type Validator = (typeof VALIDATORS)[number];

export const RULE_ID_PATTERN = /^[a-z0-9_.-]{1,128}$/;
