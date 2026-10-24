import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  base: './',
  plugins: [vue()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:9117',
      '/UI': 'http://127.0.0.1:9117',
      '/dl': 'http://127.0.0.1:9117',
      '/img': 'http://127.0.0.1:9117',
      '/bh': 'http://127.0.0.1:9117',
      '/health': 'http://127.0.0.1:9117',
    },
  },
  build: { target: 'es2020', outDir: 'dist' },
})
