import { Fragment, type ReactNode } from 'react';

import styles from './DataGrid.module.scss';

export type Column<T> = {
  key: string;
  header: ReactNode;
  render: (row: T) => ReactNode;
  width?: string;
  align?: 'left' | 'center' | 'right';
  /** Renders the cell in the mono instrument voice (ids, timestamps, counts). */
  mono?: boolean;
};

type DataGridProps<T> = {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  onRowClick?: (row: T) => void;
  emptyLabel?: string;
  /**
   * Groups consecutive rows under full-width header rows (e.g. audit records
   * by day). Rows are grouped in the order given — sort them first. The key
   * changes trigger a header; duplicate keys across "load more" appends are
   * fine as long as the flattened list stays sorted.
   */
  groupBy?: (row: T) => string;
  renderGroupHeader?: (key: string) => ReactNode;
};

export function DataGrid<T>({
  columns,
  rows,
  rowKey,
  onRowClick,
  emptyLabel,
  groupBy,
  renderGroupHeader,
}: DataGridProps<T>) {
  let lastGroup: string | undefined;

  return (
    <div className={styles.wrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.key} style={{ width: c.width, textAlign: c.align ?? 'left' }}>
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? (
            <tr>
              <td className={styles.empty} colSpan={columns.length}>
                {emptyLabel ?? '—'}
              </td>
            </tr>
          ) : (
            rows.map((row) => {
              const group = groupBy?.(row);
              const isNewGroup = groupBy != null && group !== lastGroup;
              lastGroup = group;
              return (
                <Fragment key={rowKey(row)}>
                  {isNewGroup && (
                    <tr className={styles.groupRow}>
                      <td colSpan={columns.length}>
                        {renderGroupHeader ? renderGroupHeader(group!) : group}
                      </td>
                    </tr>
                  )}
                  <tr
                    className={onRowClick ? styles.clickable : undefined}
                    onClick={onRowClick ? () => onRowClick(row) : undefined}
                  >
                    {columns.map((c) => (
                      <td
                        key={c.key}
                        className={c.mono ? styles.mono : undefined}
                        style={{ textAlign: c.align ?? 'left' }}
                      >
                        {c.render(row)}
                      </td>
                    ))}
                  </tr>
                </Fragment>
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}
