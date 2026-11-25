# 0011 — Atualização automática do agente, com assinatura própria

## Contexto

Cada versão nova do agente exigia, em cada PC: baixar de novo, instalar, conferir. Com mais de um
cliente isso não escala. Não há certificado de assinatura de código (decisão do usuário), então a
garantia de que o `.exe` novo é legítimo não pode vir do Authenticode.

## Decisão

- **Versão no build:** o `build.sh` grava `main.version` (data UTC, `AAAA.MM.DD.hhmm`) nos dois `.exe`.
- **Chave do projeto (Ed25519):** a primeira execução do `build.sh` cria a chave privada em
  `~/.config/remoteit/update-ed25519.key` (fora do repositório e **nunca** no servidor). A chave
  pública vai embutida em cada `.exe` (`main.updatePublicKey`).
- **Manifesto assinado:** o `build.sh` grava `dist/update.json` com a versão e, por edição, o SHA-256
  do `.exe` e a assinatura de `remoteit-update-v1\n<edição>\n<versão>\n<sha256>`
  (`protocol.UpdateMessage`, ferramenta `cmd/signupdate`).
- **Relay só repassa:** `GET /api/agent/update?mid=&edition=` devolve a oferta do manifesto;
  `GET /api/agent/update/{edição}?mid=` devolve o `.exe` sem código gravado. As duas exigem a
  credencial da máquina ([ADR 0010](0010-maquinas-por-tecnico.md)); agentes legados não se atualizam.
- **O agente decide** (`internal/agent/update`): ao conectar e a cada hora, compara a versão
  anunciada com a dele; se for **diferente**, confere a assinatura com a chave embutida, espera não
  haver técnico conectado (tenta de novo a cada minuto), baixa, confere o SHA-256 e aplica.
- **Aplicar** (`lifecycle.ApplyUpdate`): no serviço, o worker (SYSTEM) grava o `.exe` em
  `%ProgramData%\RemoteIT\update` e roda `install` com ele — o mesmo caminho testado de "baixar e dar
  dois cliques", agora sem UAC. No portátil, renomeia o `.exe` em uso para `.old`, grava o novo no
  lugar, solta o mutex de instância e se reabre.
- **Rollback:** "diferente", não "maior". Publicar no relay os `.exe` e o `update.json` de uma versão
  anterior faz os agentes voltarem para ela.

## Consequências

- Positivo: publicar uma versão é só trocar os arquivos de `dist/` no servidor; em até 1 hora (ou na
  próxima reconexão, se o relay reiniciar) todas as máquinas atualizam sozinhas, sem ninguém no PC.
- Positivo: um relay invadido não consegue distribuir um `.exe` próprio: sem a chave privada, a
  assinatura não confere.
- Negativo: **a chave privada é ponto único**. Perdê-la significa que os agentes instalados não
  aceitam mais nenhuma atualização (é preciso reinstalar à mão uma vez, com um build de chave nova).
  Vazá-la permite assinar `.exe` maliciosos. Guarde uma cópia offline e fora do servidor.
- Negativo: sem verificação de saúde depois da troca; se uma versão nova não conectar, o rollback é
  publicar a anterior, mas uma versão que nem chega a conectar não vai buscá-la. Por isso: testar a
  versão numa máquina antes de publicar.
- Os agentes instalados **antes** desta versão não têm o atualizador: precisam de uma última
  instalação manual.
