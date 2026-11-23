# 0008 — Duas edições do agente e nenhuma janela

## Contexto

O agente tinha um `.exe` só, com subcomandos `install`/`uninstall` e caixas de mensagem para cada
resultado ("Serviço já estava instalado", "Execute como Administrador"...). Na prática:

- usuários não leem as caixas e acham que qualquer aviso é defeito;
- `install` com o serviço já existente só o iniciava de novo, e o serviço seguia rodando o `.exe`
  antigo de Downloads sem ninguém perceber;
- o PowerShell não espera um programa de janela terminar, e `uninstall` seguido de `Stop-Process`
  matava o próprio desinstalador no meio;
- quem queria "só executar" e quem queria "instalar" baixavam o mesmo arquivo e dependiam de lembrar
  um subcomando.

## Decisão

- O build gera **duas edições do mesmo código**, distinguidas por `-X main.edition=servico`:
  `suporte-ti.exe` (dois cliques = roda) e `suporte-ti-servico.exe` (dois cliques = `install`). A
  edição fica dentro do binário, então o nome do arquivo não importa.
- O console oferece dois downloads, **Executar agente** e **Instalar como serviço**.
- **Nenhuma janela.** `install`/`uninstall` gravam o resultado em `service.log` e, se vieram de um
  terminal, escrevem nele (`AttachConsole`). Sem admin, pedem elevação pelo UAC em vez de falhar.
- `install` é **idempotente e serve para atualizar**: copia o `.exe` para `%ProgramFiles%\RemoteIT`,
  para o serviço se ele existir, troca o arquivo, atualiza o caminho e inicia.
- A edição portátil **nunca mexe no serviço**: com o serviço rodando, ou com outra cópia aberta, sai
  em silêncio.
- O relay serve os downloads com `Cache-Control: no-store, private`, e o link do console muda a cada
  carregamento.

## Consequências

- Positivo: a escolha é explícita; reinstalar ou atualizar é sempre "baixar e dar dois cliques"; o
  serviço nunca depende de um arquivo em Downloads; nenhum aviso assusta o usuário.
- Negativo: dois builds com cgo (o dobro do tempo de compilação); um erro na instalação só aparece no
  log, não na tela. Abrir o portátil num PC com o serviço instalado "não faz nada" de propósito.
