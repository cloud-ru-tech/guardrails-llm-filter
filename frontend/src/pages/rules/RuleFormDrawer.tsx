import { Alert } from '@snack-uikit/alert';
import { ButtonFilled, ButtonFunction, ButtonOutline } from '@snack-uikit/button';
import { DrawerCustom } from '@snack-uikit/drawer';
import { FieldSelect, FieldText, FieldTextArea } from '@snack-uikit/fields';
import { CrossSVG, PlaySVG } from '@snack-uikit/icons';
import { toaster } from '@snack-uikit/toaster';
import { Typography } from '@snack-uikit/typography';
import { useEffect, useMemo, useState } from 'react';

import { ApiRequestError } from '@/api/client';
import { useCreateRule, useScan, useUpdateRule } from '@/api/hooks';
import type { Rule } from '@/api/types';
import { ScanResultPanel } from '@/components/ScanResultPanel';
import { CHART_COLOR, DATA_TYPE_NAME, RULE_ID_PATTERN, VALIDATORS } from '@/domain/dataTypes';
import { t } from '@/i18n/strings';

import styles from './RuleFormDrawer.module.scss';

export type DataTypeOption = { value: number; option: string };

type Props = {
  open: boolean;
  onClose: () => void;
  /** Existing custom rule to edit; undefined creates a new one. */
  rule?: Rule;
  dataTypeOptions: DataTypeOption[];
};

type FormState = {
  ruleId: string;
  name: string;
  group: string;
  dataType: number | undefined;
  regex: string;
  keywords: string;
  validators: string[];
  minLength: string;
  entropy: string;
  banlist: string;
  placeholder: string;
  captureGroups: string;
};

const csvToList = (s: string) =>
  s
    .split(',')
    .map((x) => x.trim())
    .filter(Boolean);

const listToCsv = (l?: (string | number)[]) => (l ?? []).join(', ');

/** Colored entity dot — rendered only next to the type's text label. */
const entityDot = (id: number) => (
  <span
    className={styles.entityDot}
    style={{ background: CHART_COLOR[id] ?? 'var(--sys-neutral-text-support)' }}
    aria-hidden="true"
  />
);

function toForm(rule?: Rule): FormState {
  return {
    ruleId: rule?.rule_id ?? '',
    name: rule?.name ?? '',
    group: rule?.group ?? '',
    dataType: rule?.data_type,
    regex: rule?.regex ?? '',
    keywords: listToCsv(rule?.keywords),
    validators: rule?.validators ?? [],
    minLength: rule?.min_length != null ? String(rule.min_length) : '',
    entropy: rule?.entropy != null ? String(rule.entropy) : '',
    banlist: listToCsv(rule?.banlist),
    placeholder: rule?.masking?.placeholder ?? '',
    captureGroups: listToCsv(rule?.masking?.capture_groups),
  };
}

export function RuleFormDrawer({ open, onClose, rule, dataTypeOptions }: Props) {
  const isEdit = Boolean(rule);
  const [form, setForm] = useState<FormState>(() => toForm(rule));
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<{ ruleId?: string }>({});
  const [sample, setSample] = useState('');

  const createRule = useCreateRule();
  const updateRule = useUpdateRule();
  const scan = useScan();
  const pending = createRule.isPending || updateRule.isPending;

  const dtLabel = (id: number) =>
    dataTypeOptions.find((o) => o.value === id)?.option ?? DATA_TYPE_NAME[id] ?? String(id);

  // Entity dot next to each option label in the droplist.
  const entityOptions = useMemo(
    () => dataTypeOptions.map((o) => ({ ...o, beforeContent: entityDot(o.value) })),
    [dataTypeOptions],
  );

  // Reset the form whenever the drawer (re)opens for a different rule.
  useEffect(() => {
    if (open) {
      setForm(toForm(rule));
      setError(null);
      setFieldErrors({});
      setSample('');
      scan.reset();
    }
    // scan is stable across renders; resetting it here is intentional.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, rule]);

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }));

  const validateLocal = (): boolean => {
    const errs: { ruleId?: string } = {};
    if (!RULE_ID_PATTERN.test(form.ruleId)) errs.ruleId = t.rules.form.badRuleId;
    setFieldErrors(errs);
    return Object.keys(errs).length === 0 && Boolean(form.name && form.regex && form.dataType);
  };

  const buildRule = (): Rule => {
    const captureGroups = csvToList(form.captureGroups)
      .map(Number)
      .filter((n) => Number.isFinite(n));
    return {
      rule_id: form.ruleId,
      name: form.name,
      data_type: form.dataType as number,
      regex: form.regex,
      group: form.group || undefined,
      keywords: csvToList(form.keywords).length ? csvToList(form.keywords) : undefined,
      validators: form.validators.length ? (form.validators as Rule['validators']) : undefined,
      min_length: form.minLength ? Number(form.minLength) : undefined,
      entropy: form.entropy ? Number(form.entropy) : undefined,
      banlist: csvToList(form.banlist).length ? csvToList(form.banlist) : undefined,
      masking: {
        placeholder: form.placeholder || undefined,
        capture_groups: captureGroups.length ? captureGroups : undefined,
      },
    };
  };

  const handleSubmit = () => {
    setError(null);
    if (!validateLocal()) {
      setError(t.rules.form.required);
      return;
    }
    const body = buildRule();
    const mutation = isEdit ? updateRule : createRule;
    mutation.mutate(body, {
      onSuccess: () => {
        toaster.userAction.success({ label: isEdit ? t.rules.saved : t.rules.created });
        onClose();
      },
      onError: (err) => {
        if (err instanceof ApiRequestError) {
          setError(err.details ? `${err.message} — ${err.details}` : err.message);
        } else {
          setError(t.rules.form.serverRejected);
        }
      },
    });
  };

  const handleTest = () => {
    scan.mutate({ text: sample, candidate_rule: buildRule() });
  };

  const testError =
    scan.error instanceof ApiRequestError
      ? scan.error.details
        ? `${t.rules.test.invalid}: ${scan.error.details}`
        : `${t.rules.test.invalid}: ${scan.error.message}`
      : scan.error
        ? t.rules.test.invalid
        : null;

  return (
    <DrawerCustom open={open} onClose={onClose} size="min(560px, 100vw)">
      <div className={styles.drawer}>
        <div className={styles.header}>
          <Typography family="sans" purpose="title" size="s" tag="h2">
            {isEdit ? t.rules.form.editTitle : t.rules.form.createTitle}
          </Typography>
          <ButtonFunction icon={<CrossSVG />} aria-label={t.common.close} onClick={onClose} />
        </div>

        <div className={styles.body}>
          {error && <Alert appearance="error" icon description={error} />}

          <section className={styles.section}>
            <span className={styles.sectionTitle}>{t.rules.form.sectionMain}</span>
            <div className={styles.grid2}>
              <FieldText
                className={styles.monoField}
                label={t.rules.form.ruleId}
                inputMode="text"
                hint={fieldErrors.ruleId ?? t.rules.form.ruleIdHint}
                validationState={fieldErrors.ruleId ? 'error' : 'default'}
                value={form.ruleId}
                onChange={(v) => set('ruleId', v)}
                disabled={isEdit}
                required
              />
              <FieldText
                label={t.rules.form.name}
                inputMode="text"
                value={form.name}
                onChange={(v) => set('name', v)}
                required
              />
            </div>

            <div className={styles.grid2}>
              <FieldText
                label={t.rules.form.group}
                inputMode="text"
                value={form.group}
                onChange={(v) => set('group', v)}
              />
              <FieldSelect
                selection="single"
                label={t.rules.form.dataType}
                options={entityOptions}
                prefix={form.dataType != null ? entityDot(form.dataType) : undefined}
                value={form.dataType}
                onChange={(v) => set('dataType', v == null ? undefined : Number(v))}
                required
              />
            </div>
          </section>

          <section className={styles.section}>
            <span className={styles.sectionTitle}>{t.rules.form.sectionDetection}</span>
            <FieldTextArea
              className={styles.monoField}
              label={t.rules.form.regex}
              value={form.regex}
              onChange={(v) => set('regex', v)}
              required
            />

            <FieldText
              className={styles.monoField}
              label={t.rules.form.keywords}
              inputMode="text"
              hint={t.rules.form.keywordsHint}
              value={form.keywords}
              onChange={(v) => set('keywords', v)}
            />

            <FieldSelect
              selection="multiple"
              label={t.rules.form.validators}
              options={VALIDATORS.map((v) => ({ value: v, option: v }))}
              value={form.validators}
              onChange={(v) => set('validators', (v ?? []).map(String))}
            />

            <div className={styles.grid2}>
              <FieldText
                className={styles.monoField}
                label={t.rules.form.minLength}
                inputMode="numeric"
                value={form.minLength}
                onChange={(v) => set('minLength', v)}
              />
              <FieldText
                className={styles.monoField}
                label={t.rules.form.entropy}
                inputMode="decimal"
                value={form.entropy}
                onChange={(v) => set('entropy', v)}
              />
            </div>

            <FieldText
              className={styles.monoField}
              label={t.rules.form.banlist}
              inputMode="text"
              hint={t.rules.form.banlistHint}
              value={form.banlist}
              onChange={(v) => set('banlist', v)}
            />
          </section>

          <section className={styles.section}>
            <span className={styles.sectionTitle}>{t.rules.form.sectionMasking}</span>
            <div className={styles.grid2}>
              <FieldText
                className={styles.monoField}
                label={t.rules.form.placeholder}
                inputMode="text"
                hint={t.rules.form.placeholderHint}
                value={form.placeholder}
                onChange={(v) => set('placeholder', v)}
              />
              <FieldText
                className={styles.monoField}
                label={t.rules.form.captureGroups}
                hint={t.rules.form.captureGroupsHint}
                inputMode="numeric"
                value={form.captureGroups}
                onChange={(v) => set('captureGroups', v)}
              />
            </div>
          </section>

          <section className={styles.section}>
            <span className={styles.sectionTitle}>{t.rules.test.title}</span>
            <FieldTextArea
              className={styles.monoField}
              label={t.rules.test.sample}
              placeholder={t.rules.test.samplePlaceholder}
              value={sample}
              onChange={setSample}
              minRows={3}
            />
            <div className={styles.testActions}>
              <ButtonOutline
                label={t.rules.test.run}
                icon={<PlaySVG />}
                onClick={handleTest}
                loading={scan.isPending}
                disabled={!sample.trim() || !form.regex.trim()}
              />
            </div>
            {testError && <Alert appearance="error" icon description={testError} />}
            {scan.data && !testError && (
              <ScanResultPanel
                result={scan.data}
                dtLabel={dtLabel}
                // The candidate rule's CURRENT form data type must win over
                // the saved registry entry (edited or brand-new rule).
                ruleDataTypeOverrides={
                  form.ruleId && typeof form.dataType === 'number'
                    ? { [form.ruleId]: form.dataType }
                    : undefined
                }
              />
            )}
          </section>
        </div>

        <div className={styles.footer}>
          <ButtonOutline label={t.common.cancel} onClick={onClose} disabled={pending} />
          <ButtonFilled label={t.common.save} onClick={handleSubmit} loading={pending} />
        </div>
      </div>
    </DrawerCustom>
  );
}
