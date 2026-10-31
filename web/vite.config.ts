import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  base: '/res/cwclash/',
  build: {
    target: 'es2020',
    cssCodeSplit: false,
    rollupOptions: { output: { entryFileNames: 'app.js', chunkFileNames: '[name].js', assetFileNames: '[name][extname]' } },
  },
  server: {
    proxy: { '/_api': { target: 'http://127.0.0.1:17922' } },
  },
})
