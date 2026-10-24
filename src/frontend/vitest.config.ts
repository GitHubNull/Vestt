import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

export default defineConfig({
  // vitest 3 bundles vite 7 types while the project uses vite 8; the plugin
  // runtime is compatible, only the type identities differ.
  plugins: [vue() as unknown as import('vitest/config').Plugin],
  test: {
    setupFiles: ['./src/test-setup.ts'],
  },
})
