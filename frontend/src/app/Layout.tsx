import { ButtonTonal } from '@snack-uikit/button';
import {
  DaySVG,
  FilterSVG,
  HomeSVG,
  NightSVG,
  PlaySVG,
  SettingsSVG,
  WatchSVG,
} from '@snack-uikit/icons';
import { Typography } from '@snack-uikit/typography';
import { NavLink, Outlet } from 'react-router-dom';

import { useHealth, useVersion } from '@/api/hooks';
import { t } from '@/i18n/strings';

import styles from './Layout.module.scss';
import { useTheme } from './theme';

const NAV = [
  { to: '/overview', label: t.nav.overview, icon: <HomeSVG /> },
  { to: '/rules', label: t.nav.rules, icon: <FilterSVG /> },
  { to: '/tester', label: t.nav.tester, icon: <PlaySVG /> },
  { to: '/settings', label: t.nav.settings, icon: <SettingsSVG /> },
  { to: '/audit', label: t.nav.audit, icon: <WatchSVG /> },
];

function StatusIndicator() {
  const health = useHealth();
  const version = useVersion();

  const healthy = !health.isError && Boolean(health.data);
  const versionLabel = version.data?.version;
  const topology = version.data?.topology;

  return (
    <div className={styles.status}>
      <span
        className={`${styles.statusDot} ${healthy ? styles.statusDotHealthy : styles.statusDotUnhealthy}`}
        aria-label={healthy ? t.status.healthy : t.status.unhealthy}
        title={healthy ? t.status.healthy : t.status.unhealthy}
      />
      <Typography family="sans" purpose="body" size="s" tag="span" className={styles.statusText}>
        {versionLabel ? `${t.status.version} ${versionLabel}` : t.status.unknown}
        {topology ? ` · ${topology}` : ''}
      </Typography>
    </div>
  );
}

export function Layout() {
  const { theme, changeTheme } = useTheme();

  return (
    <div className={styles.shell}>
      <aside className={styles.sidebar}>
        <div className={styles.brand}>
          <div className={styles.brandMark}>
            <svg
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
              className={styles.brandLogo}
              aria-hidden="true"
            >
              <path fill="#26D07C" d="M22 13v5l-9 4v-9zm0-2V6l-9-4v9zM2 6v12l9 4V2z" />
            </svg>
            <Typography family="sans" purpose="title" size="l" tag="span">
              {t.app.title}
            </Typography>
          </div>
          <Typography
            family="sans"
            purpose="body"
            size="s"
            tag="span"
            data-test-id="app-subtitle"
          >
            {t.app.subtitle}
          </Typography>
        </div>

        <nav className={styles.nav}>
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                isActive ? `${styles.navLink} ${styles.navLinkActive}` : styles.navLink
              }
            >
              {item.icon}
              <Typography family="sans" purpose="label" size="l" tag="span">
                {item.label}
              </Typography>
            </NavLink>
          ))}
        </nav>
      </aside>

      <div className={styles.main}>
        <header className={styles.topbar}>
          <StatusIndicator />
          <ButtonTonal
            appearance="neutral"
            icon={theme === 'dark' ? <DaySVG /> : <NightSVG />}
            aria-label={theme === 'dark' ? t.theme.light : t.theme.dark}
            onClick={() => changeTheme(theme === 'dark' ? 'light' : 'dark')}
          />
        </header>
        <main className={styles.content}>
          <Outlet />
        </main>
      </div>
    </div>
  );
}
