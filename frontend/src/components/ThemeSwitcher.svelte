<script lang="ts">
  import { theme } from '../stores/theme';

  function toggleTheme() {
    theme.update(currentTheme => (currentTheme === 'light' ? 'dark' : 'light'));
  }
</script>

<button
  onclick={toggleTheme}
  class="theme-toggle"
  aria-label={$theme === 'light' ? 'Switch to dark theme' : 'Switch to light theme'}
  title={$theme === 'light' ? 'Switch to dark theme' : 'Switch to light theme'}
>
  {#if $theme === 'light'}
    <span aria-hidden="true">☀️</span>
  {:else}
    <span aria-hidden="true">🌙</span>
  {/if}
</button>

<style>
  .theme-toggle {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: none;
    border: none;
    border-radius: var(--radius-sm);
    cursor: pointer;
    font-size: 1.25rem;
    min-width: 1.75rem;
    min-height: 1.75rem;
    padding: 0.375rem;
    margin-right: 0.125rem;
  }
</style>