import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
const vscodeSrc = new URL('./node_modules/@codingame/monaco-vscode-api/vscode/src', import.meta.url).pathname

export default defineConfig({
  base: './',
  plugins: [
    react()
  ],
  worker: {
    format: 'es'
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: true
      }
    }
  },
  define: {
    global: 'window',
    'process.env': {}
  },
  resolve: {
    alias: {
      'monaco-editor': '@codingame/monaco-vscode-editor-api',
      '@codingame/monaco-vscode-api/vscode': vscodeSrc
    }
  },
  assetsInclude: ['**/*.wasm']
})

