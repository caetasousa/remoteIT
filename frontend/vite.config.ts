import { writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

// O build vai para dentro do pacote Go que o embute: go:embed não lê pastas
// acima do pacote.
const outDir = resolve(import.meta.dirname, '../internal/relay/web/dist');

// Relay usado pelo `npm run dev` (make dev sobe um em localhost:8080).
const relay = process.env.RELAY_DEV_URL ?? 'http://localhost:8080';

export default defineConfig({
  plugins: [
    svelte(),
    {
      // emptyOutDir apaga o .gitkeep que mantém a pasta no git (sem ela o
      // relay não compila); este plugin o recria.
      name: 'keep-gitkeep',
      closeBundle() {
        writeFileSync(resolve(outDir, '.gitkeep'), '');
      },
    },
  ],
  // limite maior: o único pedaço grande é o Three.js, carregado sob demanda
  build: { outDir, emptyOutDir: true, target: 'es2022', chunkSizeWarningLimit: 600 },
  server: {
    proxy: {
      '/api': relay,
      '/download': relay,
      '/ws': {
        target: relay,
        ws: true,
        // O relay só aceita WebSocket com Origin igual ao Host (proteção
        // contra Cross-Site WebSocket Hijacking); no dev a página vem de
        // outra porta, então o proxy reescreve o Origin.
        configure: (proxy) => proxy.on('proxyReqWs', (req) => req.setHeader('origin', relay)),
      },
    },
  },
});
