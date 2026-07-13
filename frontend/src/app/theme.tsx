import { useThemeConfig } from '@snack-uikit/utils';
import { createContext, useContext, useEffect, type ReactNode } from 'react';

// brand.module.css exports the `.light` / `.dark` token classes as CSS-module names.
import brand from '@snack-uikit/figma-tokens/build/css/brand.module.css';

export type ThemeName = 'light' | 'dark';

type ThemeContextValue = {
  theme: ThemeName;
  changeTheme: (theme: ThemeName) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

const STORAGE_KEY = 'guardrails-ui.theme';

function initialTheme(): ThemeName {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === 'light' || stored === 'dark') return stored;
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const { theme, themeClassName, changeTheme } = useThemeConfig<ThemeName>({
    themeMap: { light: brand.light, dark: brand.dark },
    defaultTheme: initialTheme(),
  });

  const handleChange = (next: ThemeName) => {
    localStorage.setItem(STORAGE_KEY, next);
    changeTheme(next);
  };

  // Apply the theme token classes to <body> (not an inner div) so snack-uikit
  // overlays — Drawer/Modal/Tooltip — which portal into document.body still
  // receive the --sys-* / font CSS variables. `gr-theme` is our stable hook for
  // the font + chart-color overrides in theme-overrides.scss.
  useEffect(() => {
    const { body } = document;
    body.classList.add('gr-theme');
    if (themeClassName) body.classList.add(themeClassName);
    body.setAttribute('data-theme', theme);
    return () => {
      if (themeClassName) body.classList.remove(themeClassName);
    };
  }, [themeClassName, theme]);

  return (
    <ThemeContext.Provider value={{ theme, changeTheme: handleChange }}>
      <div className="appShell">{children}</div>
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within ThemeProvider');
  return ctx;
}
