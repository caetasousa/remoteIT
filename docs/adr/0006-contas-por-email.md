# 0006 — Contas por e-mail com aprovação

## Contexto

O admin inicial vinha de uma senha no ambiente, só um admin criava contas e não havia recuperação. O
console é público na internet e qualquer conta ativa controla todas as máquinas e baixa o agente,
que tem a chave embutida.

## Decisão

- **E-mail confirmado não basta:** a conta nasce pendente e um admin aprova. A exceção é o
  `RELAY_ADMIN_EMAIL`, que permite criar o primeiro admin sem senha no ambiente.
- **A senha é escolhida depois do link.** Se fosse no pedido, alguém cadastraria o e-mail do admin
  com uma senha própria e viraria admin quando ele clicasse em confirmar (*account pre-hijacking*).
- **O vínculo de e-mail exige a senha e a sessão de quem pediu.** Sem isso, o clique de um terceiro
  ligaria o e-mail dele à conta de outra pessoa.
- **Tokens** de 256 bits, só o SHA-256 no banco, uso único garantido por `UPDATE ... RETURNING` na
  transação da mudança. Vão no fragmento da URL, que não chega a logs de servidor. Só um POST os
  consome: antivírus que abre links não os gasta.
- **Resposta igual exista ou não a conta:** o pedido entra numa fila e responde 202 antes de
  consultar o banco, então nem o tempo de resposta revela.
- **Limites por IP, por destinatário e por dia**, separados por tipo, porque a cota SMTP é dividida
  com outros sistemas.
- **Trocar a senha ou remover a conta derruba também o WebSocket aberto**, que não passa de novo
  pela autenticação.
- **go-mail** para MIME, STARTTLS e cabeçalhos, em vez de montar a mensagem à mão.

Considerado: cadastro aberto sem aprovação (entregaria as máquinas a qualquer pessoa), lista de
domínios permitidos (não serve para quem usa e-mail pessoal) e fila de e-mail no banco como a do
StakTrack (mais código; aqui basta pedir o link de novo).

## Consequências

- Positivo: criação e recuperação sem editar o servidor, sem ampliar quem acessa as máquinas.
- Negativo: a fila e o limite por IP ficam em memória e se perdem ao reiniciar.
- Negativo: quem esgotar o teto diário de cadastros impede novos cadastros por até 24 h; a
  recuperação tem teto próprio e continua.
- Negativo: quem controla a caixa de e-mail controla a conta; a do admin merece 2FA.
