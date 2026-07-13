import { Alert } from '@snack-uikit/alert';
import { Spinner } from '@snack-uikit/loaders';
import type { ReactNode } from 'react';

import { ApiRequestError } from '@/api/client';
import { t } from '@/i18n/strings';

import styles from './Page.module.scss';

function messageFor(error: unknown): string {
  if (error instanceof ApiRequestError) {
    if (error.status === 401) return t.common.unauthorized;
    return error.details ? `${error.message} — ${error.details}` : error.message;
  }
  return error instanceof Error ? error.message : String(error);
}

export function QueryBoundary({
  isLoading,
  error,
  onRetry,
  children,
}: {
  isLoading: boolean;
  error: unknown;
  onRetry?: () => void;
  children: ReactNode;
}) {
  if (isLoading) {
    return (
      <div className={styles.center}>
        <Spinner size="m" />
      </div>
    );
  }

  if (error) {
    return (
      <Alert
        appearance="error"
        title={t.common.errorTitle}
        description={messageFor(error)}
        actions={onRetry ? { primary: { text: t.common.retry, onClick: onRetry } } : undefined}
      />
    );
  }

  return <>{children}</>;
}
