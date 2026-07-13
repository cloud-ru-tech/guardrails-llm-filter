import { Spinner } from '@snack-uikit/loaders';
import { lazy, Suspense } from 'react';
import { Navigate, Route, BrowserRouter, Routes } from 'react-router-dom';

import { AuditPage } from '@/pages/audit/AuditPage';
import { RulesPage } from '@/pages/rules/RulesPage';
import { SettingsPage } from '@/pages/settings/SettingsPage';
import { TesterPage } from '@/pages/tester/TesterPage';

import { Layout } from './Layout';
import { Providers } from './Providers';

// Code-split the dashboard so its charting library (recharts) is only fetched
// when the Overview route is visited, keeping the initial bundle lean.
const OverviewPage = lazy(() =>
  import('@/pages/overview/OverviewPage').then((m) => ({ default: m.OverviewPage })),
);

const routeFallback = (
  <div style={{ display: 'flex', justifyContent: 'center', padding: '64px 0' }}>
    <Spinner size="m" />
  </div>
);

export function App() {
  return (
    <Providers>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<Navigate to="/overview" replace />} />
            <Route
              path="/overview"
              element={
                <Suspense fallback={routeFallback}>
                  <OverviewPage />
                </Suspense>
              }
            />
            <Route path="/rules" element={<RulesPage />} />
            <Route path="/tester" element={<TesterPage />} />
            <Route path="/settings" element={<SettingsPage />} />
            <Route path="/audit" element={<AuditPage />} />
            <Route path="*" element={<Navigate to="/overview" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </Providers>
  );
}
