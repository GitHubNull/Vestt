<script setup lang="ts">
import LogViewer from '../components/settings/LogViewer.vue'
</script>

<template>
  <div class="settings-page">
    <h1 class="page-title">{{ $t('logs.title') }}</h1>
    <div class="glass-card settings-card">
      <LogViewer />
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  max-width: 72rem;
  margin: 0 auto;
}
.page-title {
  margin: 0 0 1rem;
  font-size: 1.5rem;
}
.settings-card {
  padding: 1.25rem;
}
</style>
