// Confirmações do console num modal próprio, no lugar do confirm() do
// navegador (que trava a página, não segue o tema e, em tela cheia, tira o
// técnico da tela remota). Uso:
//
//   if (await ask({ title: 'Remover PC-01?', message: '…', confirm: 'Remover', danger: true })) …
//
// O modal fica em components/layout/ConfirmDialog.svelte, montado uma vez no App.

export interface ConfirmOptions {
  title: string;
  message?: string;
  /** Texto do botão que confirma (padrão "Confirmar"). */
  confirm?: string;
  /** Ação destrutiva: botão em vermelho e foco inicial no "Cancelar". */
  danger?: boolean;
}

interface Pending extends ConfirmOptions {
  resolve: (ok: boolean) => void;
}

/** O pedido aberto agora; null com o modal fechado. */
export const confirmState = $state<{ current: Pending | null }>({ current: null });

/** Abre o modal e resolve true se o técnico confirmar. Um pedido novo cancela o anterior. */
export function ask(o: ConfirmOptions): Promise<boolean> {
  confirmState.current?.resolve(false);
  return new Promise((resolve) => {
    confirmState.current = { ...o, resolve };
  });
}

/** Fecha o modal com a resposta. */
export function answer(ok: boolean) {
  const c = confirmState.current;
  confirmState.current = null;
  c?.resolve(ok);
}
