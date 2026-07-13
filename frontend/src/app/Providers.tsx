import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { Sprite, SpriteSVG } from '@snack-uikit/icons';
import { ToasterContainer } from '@snack-uikit/toaster';
import { useState, type ReactNode } from 'react';

import { ThemeProvider } from './theme';

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 10_000 },
        },
      }),
  );

  return (
    <QueryClientProvider client={queryClient}>
      <ThemeProvider>
        {/* Injects the @snack-uikit/icons SVG sprite once. Without it every icon
            (rendered as <use href="#snack-uikit-…">) resolves to nothing. */}
        <Sprite content={SpriteSVG} />
        {children}
        <ToasterContainer />
      </ThemeProvider>
    </QueryClientProvider>
  );
}
