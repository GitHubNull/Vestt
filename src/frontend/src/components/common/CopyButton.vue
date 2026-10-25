<script setup lang="ts">
import Button from 'primevue/button'
import { notify } from './notify'

// Upstream copyToClipboard (custom.js): writes the text and reports success
// via a toast. Modern-first with the upstream execCommand fallback.
const props = defineProps<{ text: string; title?: string }>()

async function copy() {
  let copied = false
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(props.text)
      copied = true
    } catch {
      copied = false
    }
  }
  if (!copied) {
    copied = legacyCopy(props.text)
  }
  if (copied) notify.success('Copied to clipboard!')
}

function legacyCopy(text: string): boolean {
  const el = document.createElement('textarea')
  el.id = '_hiddenCopyText_'
  el.value = text
  el.style.position = 'fixed'
  el.style.left = '-9999px'
  el.style.top = '0'
  document.body.appendChild(el)
  const previous = document.activeElement as HTMLElement | null
  el.focus()
  el.setSelectionRange(0, el.value.length)
  let ok: boolean
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  previous?.focus()
  document.body.removeChild(el)
  return ok
}
</script>

<template>
  <Button
    icon="pi pi-copy"
    :title="title ?? 'Copy to clipboard'"
    severity="secondary"
    size="small"
    @click="copy"
  />
</template>
