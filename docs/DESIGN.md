# 🎨 Design do console

Tema "Terminal Tático": console de operações estilo terminal/shell, fundo quase preto com tom azulado
e acento ciano, tudo em fonte monoespaçada. Layout de entrada (mensagem grande à esquerda, caixa de
login compacta no canto direito) herdado da iteração anterior do tema; a linguagem visual (titlebar
com os três pontos, prompt `$`, cores) é nova.

## 🎨 Tokens (`frontend/src/app.css`)

| Papel | Valor |
| --- | --- |
| Palco / superfície / painel | `#020408` / `#060a12` / `#0a0e16` |
| Acento | `#00f0ff` |
| Texto forte / normal / apoio / rótulo | `#f1f5f9` / `#c7d2d8` / `#5b6b78` / `#44525c` |
| Réguas | ciano a 7%, 14% e 35% |
| Raio | 0 px (bordas retas, estilo terminal) |
| Fonte | JetBrains Mono em tudo — não há uma segunda família |

O rótulo mais escuro (`#44525c`) ainda passa 4,5:1 de contraste sobre os três fundos.

## 🧱 Estrutura (`frontend/src/components/layout/`)

| Componente | Papel |
| --- | --- |
| `Frame` | Moldura de 1280 px com titlebar de shell (`root@relay:~/console$` + pontos coloridos) e cabeçalho em três colunas |
| `Entrance` | Entrada: mensagem (3/4) e formulário no trilho (1/4) |
| `Sequence` | Seção: trilho com `$ comando` e leituras (1/4) e trabalho (3/4) |
| `ConfirmDialog` | Modal de confirmação (`<dialog>` nativo), aberto por `ask()` de `lib/confirm.svelte.ts` |

## 📏 Regras

- **Leitura é dado real.** Nada de latência, contador ou status decorativo inventado.
- **Nada de efeito junto do vídeo remoto.** O Viewer não tem animação, e nenhum ancestral do cursor
  remoto pode ter `transform`, `filter` ou `backdrop-filter`: ele usa `position: fixed`.
- **O canvas é dimensionado em JS** (`fitCanvas`), não com `object-fit`: a caixa do elemento tem de
  continuar igual à imagem para o mapeamento de clique e cursor (`getBoundingClientRect`) valer.
- **Movimento respeita `prefers-reduced-motion`.**
- **Nada de CDN:** a fonte vai no binário, para funcionar atrás do firewall corporativo.
- **Tabela no celular:** cada `td` precisa de `data-label`.
- **Nada de `alert`/`confirm`/`prompt` do navegador.** Confirmações usam `await ask({title, message,
  confirm, danger})`, que abre o `ConfirmDialog`: segue o tema, não trava a página e aparece por cima
  da tela remota em tela cheia. Ações destrutivas (`danger`) têm o botão em vermelho e o foco começa
  em **Cancelar**, para um Enter não confirmar sem querer. Esc ou clique fora cancelam.
- **Sem WebGL.** A iteração anterior tinha um objeto 3D decorativo na entrada (Three.js); este tema
  não usa — `frontend/src/lib/effects/` e a dependência `three` foram removidos.

## 🖵 Tela de acesso remoto (Viewer)

A barra do topo tem **48 px** e todos os controles a mesma caixa de **32 px** de altura, para nada
ficar desalinhado:

```
┌──────────┬──────────────────────┬─────┬────┬────┬────────────┬─────────────────────────────────┐
│ ← Voltar │ Qualidade ▾          │ 1:1 │ ⌨  │ 🔒 │ Tela cheia │ Codec  Resolução  FPS  Banda RTT│
└──────────┴──────────────────────┴─────┴────┴────┴────────────┴─────────────────────────────────┘
   texto      select (aria-label)  Tamanho Ctrl+  Bloquear  primário   chips de telemetria
   visível                         real    Shift+Esc (modal)
```

- `← Voltar` mantém o texto visível: o E2E procura o botão por esse nome acessível.
- Os botões quadrados têm o nome em `.sr-only` para leitores de tela.
- `1:1` alterna entre **ajustar à janela** e **tamanho real** (um pixel remoto por pixel do monitor,
  com rolagem). Ligado, usa `aria-pressed="true"`, a cor de acento e troca o rótulo para `⤢`
  (voltar a ajustar). O E2E o procura pelo nome acessível "Tamanho real".
- O canvas não é centralizado por flex: `fitCanvas` calcula tamanho e margens em pixels físicos
  inteiros, porque meio pixel de deslocamento já borra o texto.
- `data-testid="viewer-status"` e `data-testid="screen"` são usados pelos testes e não podem sumir.

## ⬇️ Downloads do agente

O trilho da lista de máquinas tem **dois** botões, cada um com uma linha de apoio:
**Executar agente** ("Dois cliques e roda. Sem instalar, sem admin.") e **Instalar como serviço**
("Acesso antes do login e na tela de bloqueio. Pede admin."). O link leva `?t=<agora>`, que muda a
cada carregamento, para nenhum cache entregar um agente antigo.
