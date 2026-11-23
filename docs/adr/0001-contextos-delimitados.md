# 0001 — Pacotes por contexto delimitado em `internal/`

## Contexto

O código original tinha tudo em `package main`, em dois diretórios. Os arquivos não eram grandes
(o maior tinha ~340 linhas), mas cada um misturava assuntos: `platform_windows.go` tinha captura,
input, consentimento e instância única; `stream.go` tinha protocolo, controle de fluxo e bitrate.
O relay guardava estado em variáveis globais. Nada disso era testável isoladamente.

## Decisão

Dividir por responsabilidade em pacotes de `internal/`, um por contexto (veja a tabela em
[README.md](README.md)), com o `main` só montando as peças. Não adotar camadas formais de DDD
(domain/application/infrastructure): para ~3 mil linhas, essa cerimônia custaria mais do que
ajudaria. A separação que importa é **regra de negócio × infraestrutura**, e ela aparece como
interfaces pequenas definidas por quem as usa.

## Consequências

- Positivo: as regras (1 técnico por máquina, sem consentimento sem vídeo, sem auditoria sem acesso)
  têm um único lugar e testes próprios; o agente compila e é testado no Linux.
- Positivo: `internal/` impede que outros módulos importem esses pacotes por acidente.
- Negativo: mais arquivos e imports. Uma mudança no protocolo toca `protocol`, `media` e o frontend.
