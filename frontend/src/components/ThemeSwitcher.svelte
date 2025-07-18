<script lang="ts">
  import { theme } from '../stores/theme';

  function toggleTheme() {
    theme.update(currentTheme => (currentTheme === 'light' ? 'dark' : 'light'));
  }
</script>

<button onclick={toggleTheme} class="theme-toggle">
  {#if $theme === 'light'}
    <span>☀️</span>
  {:else}
    <span>🌙</span>
  {/if}
</button>

<style>
  .theme-toggle {
    background: none;
    border: none;
    cursor: pointer;
    font-size: 1.25rem;
    padding: 0.375rem;
    margin-right: 0.125rem;
  }
</style>