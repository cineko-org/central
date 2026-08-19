import { copyFileSync } from 'node:fs';
import { resolve } from 'node:path';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

const outputDirectory = '../internal/central/api/assets';

export default defineConfig({
  base: '/assets/',
  plugins: [
    react(),
    {
      name: 'copy-central-font',
      closeBundle() {
        copyFileSync(
          resolve('node_modules/pretendard/dist/web/variable/woff2/PretendardVariable.woff2'),
          resolve(outputDirectory, 'PretendardVariable.woff2'),
        );
      },
    },
  ],
  build: {
    outDir: outputDirectory,
    emptyOutDir: true,
    rollupOptions: {
      input: resolve('central.html'),
      output: {
        entryFileNames: 'central.js',
        chunkFileNames: 'central-[name].js',
        assetFileNames: (asset) => asset.names.some((name) => name.endsWith('.css')) ? 'central.css' : 'central-[name][extname]',
      },
    },
  },
});
