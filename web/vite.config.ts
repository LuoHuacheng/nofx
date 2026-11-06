import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'

const chartRevision = createHash('sha256')
  .update(readFileSync(new URL('./public/planning/chart.js', import.meta.url)))
  .digest('hex')
  .slice(0, 16)

export default defineConfig({
  plugins: [react()],
  define: { __PLANNING_CHART_VERSION__: JSON.stringify(chartRevision) },
  server: {
    host: '127.0.0.1',
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
