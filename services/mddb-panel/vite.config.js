import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import fs from 'fs'

// Resolve scripts dir: Docker puts it at ./scripts/, local dev at ../../scripts/
const dockerScripts = path.resolve(import.meta.dirname, 'scripts')
const localScripts = path.resolve(import.meta.dirname, '../../scripts')
const scriptsDir = fs.existsSync(path.join(dockerScripts, 'mddb_model.py')) ? dockerScripts : localScripts

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@scripts': scriptsDir,
    },
  },
  server: {
    port: 3000,
    host: true,
    fs: {
      allow: ['.', scriptsDir],
    },
    proxy: {
      '/v1': {
        target: process.env.MDDB_SERVER || 'http://localhost:11023',
        changeOrigin: true,
      }
    }
  },
  build: {
    outDir: 'dist',
    sourcemap: true,
  }
})
