# 0005 — Testes de integração e E2E com dependências reais

## Contexto

Os erros que mais importam aqui aparecem na combinação das partes: um quadro que não atravessa o
relay, um ACK que não destrava o fluxo, uma sessão que continua válida depois de o usuário ser
removido. Mocks de banco e de WebSocket não pegam esse tipo de erro.

## Decisão

Formato "troféu": poucos testes unitários (lógica pura), a maior parte em **integração** e alguns
**E2E** nos fluxos críticos. Detalhes em [../TESTES.md](../TESTES.md).

- Integração com **PostgreSQL real** via testcontainers, **relay real** (`httptest`) e **agente real**
  (relayclient + session + media + JPEG), atrás da build tag `integration`.
- O agente roda no Linux graças à tela sintética de `capture_other.go`.
- Fakes só onde o recurso real não existe no CI: SendInput (`recordingInjector`), caixa de
  consentimento e tela.
- E2E com **Playwright** (Chromium) contra o `compose.e2e.yaml`: relay, banco e agente em containers.

## Consequências

- Positivo: os testes exercitam o mesmo caminho da produção, incluindo migrations e SQL.
- Negativo: integração e E2E precisam de Docker e levam de dezenas de segundos a minutos.
- Limite: captura GDI/DXGI, SendInput, serviço Windows e H.264 na GPU não rodam em CI Linux; o E2E
  cobre o caminho JPEG. Esses pontos ficam no checklist manual de [../TESTES.md](../TESTES.md).
