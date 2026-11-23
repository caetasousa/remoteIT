// Rotas que chegam por hash: só as dos links de conta enviados por e-mail.

export type LinkKind = 'signup' | 'reset' | 'link';
export interface AccountLink {
  kind: LinkKind;
  token: string;
}

const routes: Record<string, LinkKind> = {
  '#/criar-conta': 'signup',
  '#/nova-senha': 'reset',
  '#/confirmar-email': 'link',
};

/** Lê o link e tira o token da barra de endereço e do histórico. */
export function takeAccountLink(): AccountLink | null {
  const [route = '', query] = location.hash.split('?');
  const kind = routes[route];
  const token = new URLSearchParams(query).get('token');
  if (!kind || !token) return null;
  history.replaceState(null, '', location.pathname);
  return { kind, token };
}
