#!/usr/bin/env bash
# Gera os binários de produção em dist/:
#   dist/suporte-ti.exe          agente Windows portátil (H.264 na GPU se o Zig estiver instalado)
#   dist/suporte-ti-servico.exe  o mesmo agente, edição que instala o serviço com dois cliques
#   dist/relay            relay Linux, com o console Svelte embutido
#
# Uso: RELAY=wss://suporte.seudominio.com.br AGENT_KEY=suachave ./build.sh
# Requer Go e Node. Para H.264 requer Zig (compilador C cruzado p/ Windows):
#   https://ziglang.org/download  (ou defina ZIG=/caminho/zig)
# Alternativa sem instalar Zig: o alvo agent-windows do Dockerfile.
set -euo pipefail
: "${RELAY:?defina RELAY=wss://seu-dominio}" "${AGENT_KEY:?defina AGENT_KEY}"
ZIG="${ZIG:-$(command -v zig || echo "$HOME/.local/zig/zig")}"
cd "$(dirname "$0")"
rm -rf dist && mkdir -p dist
LDF="-s -w -H=windowsgui -X main.relayURL=$RELAY -X main.agentKey=$AGENT_KEY"

# NO_CONSENT=1 embute -noconsent: o agente transmite sem pedir "Permitir
# acesso remoto?". Use só com o consentimento/aviso da empresa; o acesso
# continua na auditoria. Sem a variável, o agente pergunta (padrão seguro).
if [ "${NO_CONSENT:-}" = "1" ]; then
  echo "!! NO_CONSENT=1: agente NÃO vai pedir confirmação ao usuário"
  LDF="$LDF -X main.defaultNoConsent=1"
fi

echo ">> console (Svelte)"
(cd frontend && npm ci --silent && npm run build --silent)

if [ -x "$ZIG" ]; then
  echo ">> agente Windows com H.264 GPU (cgo + zig)"
  CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
  CC="$ZIG cc -target x86_64-windows-gnu" CXX="$ZIG c++ -target x86_64-windows-gnu" \
  CGO_CFLAGS="-O2 -Wno-missing-braces" \
    go build -trimpath -ldflags "$LDF -extldflags=-Wl,--subsystem,windows" -o dist/suporte-ti.exe ./cmd/agent
  CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
  CC="$ZIG cc -target x86_64-windows-gnu" CXX="$ZIG c++ -target x86_64-windows-gnu" \
  CGO_CFLAGS="-O2 -Wno-missing-braces" \
    go build -trimpath -ldflags "$LDF -X main.edition=servico -extldflags=-Wl,--subsystem,windows" -o dist/suporte-ti-servico.exe ./cmd/agent
else
  echo "!! zig não encontrado: agente SEM H.264 (só JPEG). Instale o zig para a versão rápida."
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDF" -o dist/suporte-ti.exe ./cmd/agent
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDF -X main.edition=servico" -o dist/suporte-ti-servico.exe ./cmd/agent
fi

echo ">> relay Linux"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/relay ./cmd/relay
ls -lh dist
