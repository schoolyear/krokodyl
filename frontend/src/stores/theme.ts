import { writable } from 'svelte/store';

type Theme = 'light' | 'dark';

const createThemeStore = () => {
  const { subscribe, set, update } = writable<Theme>('dark');

  const applyTheme = (theme: Theme) => {
    if (typeof document === 'undefined') return;
    if (theme === 'light') {
      document.documentElement.classList.add('light');
    } else {
      document.documentElement.classList.remove('light');
    }
  };

  return {
    subscribe,
    set: (theme: Theme) => {
      localStorage.setItem('theme', theme);
      applyTheme(theme);
      set(theme);
    },
    update: (updater: (value: Theme) => Theme) => {
      update((currentTheme) => {
        const newTheme = updater(currentTheme);
        localStorage.setItem('theme', newTheme);
        applyTheme(newTheme);
        return newTheme;
      });
    },
    init: () => {
      if (typeof window === 'undefined') return;
      const storedTheme = localStorage.getItem('theme') as Theme | null;
      const systemTheme = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
      const initialTheme = storedTheme || systemTheme;
      applyTheme(initialTheme);
      set(initialTheme);
    }
  };
};

export const theme = createThemeStore();