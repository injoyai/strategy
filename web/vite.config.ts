import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { viteStaticCopy } from 'vite-plugin-static-copy'

export default defineConfig({
  base: './',
  plugins: [
    react(),
    viteStaticCopy({
      targets: [
        {
          src: 'node_modules/@codingame/monaco-vscode-theme-defaults-default-extension/extension/**',
          dest: 'extensions/theme-defaults'
        }
      ]
    })
  ],
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
      'monaco-editor': '@codingame/monaco-vscode-editor-api'
    }
  },
  assetsInclude: ['**/*.wasm']
})

