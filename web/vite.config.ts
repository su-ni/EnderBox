import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/api': {
        // 守护进程地址。默认 23333；本机起在别的端口时用 SB_API 覆盖：
        //   SB_API=http://localhost:23456 npm run dev
        target: process.env.SB_API || 'http://localhost:23333',
        changeOrigin: true,
      },
    },
  },
})
