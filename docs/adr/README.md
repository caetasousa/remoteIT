# 📝 Decisões de arquitetura (ADRs)

> 🗣️ **Em palavras simples:** cada arquivo aqui conta **uma decisão**: o problema que existia, o que foi escolhido e o preço da escolha. Serve para quem chega depois não refazer uma discussão já resolvida, nem desfazer sem querer algo que tinha motivo.

Formato: **Contexto** (o problema), **Decisão** (o que foi feito) e **Consequências** (o que se ganha e o que se perde). Uma decisão nova que substitui outra ganha um número novo, e a antiga é marcada como substituída.

| # | Decisão | Em uma frase |
|---|---|---|
| [0001](0001-contextos-delimitados.md) | 🧩 Pacotes por contexto delimitado | cada pacote em `internal/` tem um assunto só, e as fronteiras são explícitas |
| [0002](0002-postgres-usuarios-auditoria.md) | 🐘 PostgreSQL para usuários e auditoria | o que precisa sobreviver vai para o banco; quem está online fica em memória |
| [0003](0003-svelte-no-console.md) | 🧡 Svelte no console | o framework cuida das telas; o caminho dos quadros de vídeo fica fora dele |
| [0004](0004-websocket-sobre-tcp.md) | 📡 WebSocket sobre TCP 443 | passa em qualquer firewall; o controle de fluxo compensa o TCP |
| [0005](0005-testes-com-dependencias-reais.md) | 🧪 Testes com dependências reais | banco, relay e agente de verdade, em vez de mocks |
| [0006](0006-contas-por-email.md) | ✉️ Contas por e-mail | cadastro com confirmação e aprovação; recuperação por link de uso único |
| [0007](0007-modo-servico-system.md) | 🛡️ Worker SYSTEM seguindo o desktop | o único jeito de ver e controlar a tela de bloqueio e de login |
| [0008](0008-duas-edicoes-do-agente.md) | 📦 Duas edições e nenhuma janela | "executar" e "instalar" são downloads diferentes, e nada mostra aviso |
