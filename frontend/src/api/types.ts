import type { components } from './schema';

export type Rule = components['schemas']['Rule'];
export type RulePatch = components['schemas']['RulePatch'];
export type Settings = components['schemas']['Settings'];
export type DataType = components['schemas']['DataType'];
export type AuditRecord = components['schemas']['AuditRecord'];
export type AuditReplacement = components['schemas']['AuditReplacement'];
export type AuditRecordList = components['schemas']['AuditRecordList'];
export type ApiError = components['schemas']['Error'];

export type ScanRequest = components['schemas']['ScanRequest'];
export type ScanResponse = components['schemas']['ScanResponse'];
export type Placeholder = components['schemas']['Placeholder'];
export type Version = components['schemas']['Version'];
export type Health = components['schemas']['Health'];
export type MetricsSummary = components['schemas']['MetricsSummary'];
export type LabelCount = components['schemas']['LabelCount'];
export type Latency = components['schemas']['Latency'];
export type BulkPatch = components['schemas']['BulkPatch'];
export type BulkPatchResult = components['schemas']['BulkPatchResult'];
export type BulkPatchItem = components['schemas']['BulkPatchItem'];

export type RuleSource = NonNullable<Rule['source']>;

/** Audit records list query params (mirrors GET /v1/audit/records). */
export type AuditQuery = {
  model?: string;
  path?: string;
  rule_id?: string;
  data_type?: string;
  since?: string;
  until?: string;
  limit?: number;
  cursor?: string;
};
