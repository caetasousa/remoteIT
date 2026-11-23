# syntax=docker/dockerfile:1
#
# Alvos:
#   relay          imagem de produção do relay (padrão do compose.yaml)
#   agent-sim      agente Linux com tela sintética, para os testes E2E
#   agent-windows  só o .exe do agente com H.264 (CGO + Zig), exportado com --output:
#
#     RELAY=wss://dominio AGENT_KEY=... docker build --target agent-windows \
#       --secret id=relay,env=RELAY --secret id=agent_key,env=AGENT_KEY --output dist .

ARG GO_VERSION=1.27.4
ARG NODE_VERSION=26
ARG ZIG_VERSION=0.17.1

# ---------- frontend (Svelte) ----------
FROM node:${NODE_VERSION}-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# o vite grava em ../internal/relay/web/dist
RUN mkdir -p /src/internal/relay/web/dist && npm run build

# ---------- base Go ----------
FROM golang:${GO_VERSION}-alpine AS gobase
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal

# ---------- relay ----------
FROM gobase AS relay-build
COPY --from=frontend /src/internal/relay/web/dist ./internal/relay/web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/relay ./cmd/relay

FROM gcr.io/distroless/static-debian13:nonroot AS relay
COPY --from=relay-build /out/relay /relay
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
  CMD ["/relay", "-healthcheck", "-addr", ":8080"]
ENTRYPOINT ["/relay", "-insecure", "-addr", ":8080"]

# ---------- agente Linux (testes E2E) ----------
FROM gobase AS agent-sim-build
RUN CGO_ENABLED=0 go build -trimpath -o /out/agent ./cmd/agent

FROM gcr.io/distroless/static-debian13:nonroot AS agent-sim
COPY --from=agent-sim-build /out/agent /agent
ENTRYPOINT ["/agent", "-noconsent"]

# ---------- agente Windows com H.264 ----------
FROM gobase AS agent-windows-build
ARG ZIG_VERSION
RUN apk add --no-cache curl xz \
 && curl -sSfL https://ziglang.org/download/${ZIG_VERSION}/zig-x86_64-linux-${ZIG_VERSION}.tar.xz | tar -xJ -C /opt \
 && ln -s /opt/zig-x86_64-linux-${ZIG_VERSION}/zig /usr/local/bin/zig
# secrets em vez de --build-arg: não ficam no histórico da imagem
RUN --mount=type=secret,id=relay,env=RELAY,required=true \
    --mount=type=secret,id=agent_key,env=AGENT_KEY,required=true \
    CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
    CC="zig cc -target x86_64-windows-gnu" CXX="zig c++ -target x86_64-windows-gnu" \
    CGO_CFLAGS="-O2 -Wno-missing-braces" \
    go build -trimpath \
      -ldflags "-s -w -H=windowsgui -X main.relayURL=$RELAY -X main.agentKey=$AGENT_KEY -extldflags=-Wl,--subsystem,windows" \
      -o /out/suporte-ti.exe ./cmd/agent

FROM scratch AS agent-windows
COPY --from=agent-windows-build /out/suporte-ti.exe /
