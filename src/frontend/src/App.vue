<script setup lang="ts">
import DynamicDialog from 'primevue/dynamicdialog'
import Toast from 'primevue/toast'
import { useToast } from 'primevue/usetoast'
import { onMounted } from 'vue'
import AppShell from './components/common/AppShell.vue'
import { initNotify } from './components/common/notify'

// Wire the Toast adapter for non-component code (stores, api layer).
onMounted(() => {
  initNotify(useToast())
})
</script>

<template>
  <AppShell />
  <Toast position="bottom-center" />
  <DynamicDialog />
</template>
