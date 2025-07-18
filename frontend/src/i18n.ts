import { register, init, getLocaleFromNavigator } from 'svelte-i18n';

const SUPPORTED = ['en', 'nl', 'fr', 'es', 'hu', 'zh'];

// Register the loaders for your locales
register('en', () => import('./locales/en.json'));
register('nl', () => import('./locales/nl.json'));
register('fr', () => import('./locales/fr.json'));
register('es', () => import('./locales/es.json'));
register('hu', () => import('./locales/hu.json'));
register('zh', () => import('./locales/zh.json'));

// Create and export an async function to initialize i18n
export async function setupi18n() {
    const sysLocale = getLocaleFromNavigator()?.split('-')[0];

    // The init function returns a promise that resolves when the initial locale is loaded
    await init({
        fallbackLocale: 'en',
        initialLocale: SUPPORTED.includes(sysLocale) ? sysLocale : 'en',
    });
}

export const supportedLocales = SUPPORTED;