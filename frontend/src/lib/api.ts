// Cliente da API JSON do relay (internal/relay/httpapi).

export type Role = 'admin' | 'technician';

export interface User {
  id: number;
  username: string;
  email?: string;
  pending: boolean;
  role: Role;
  createdAt: string;
}

export interface AgentInfo {
  id: string;
  host: string;
  user: string;
  since: string;
  busy: boolean;
  viewedBy?: string;
}

export interface AuditEntry {
  id: number;
  userId: number;
  username: string;
  agentId: string;
  host: string;
  remoteAddr: string;
  startedAt: string;
  endedAt?: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

/** Chamado quando a sessão expira durante o uso (resposta 401). */
let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      // corpo sem JSON: fica o statusText
    }
    if (res.status === 401 && path !== '/api/login' && path !== '/api/me') onUnauthorized();
    throw new ApiError(res.status, msg);
  }
  return res.status === 202 || res.status === 204 ? (undefined as T) : res.json();
}

export const api = {
  healthy: () =>
    fetch('/healthz').then(
      (r) => r.ok,
      () => false,
    ),
  me: () => request<User>('GET', '/api/me'),
  login: (username: string, password: string) => request<User>('POST', '/api/login', { username, password }),
  logout: () => request<void>('POST', '/api/logout'),
  agents: () => request<AgentInfo[]>('GET', '/api/agents'),
  users: () => request<User[]>('GET', '/api/users'),
  createUser: (username: string, password: string, role: Role) =>
    request<User>('POST', '/api/users', { username, password, role }),
  deleteUser: (id: number) => request<void>('DELETE', `/api/users/${id}`),
  approveUser: (id: number) => request<User>('POST', `/api/users/${id}/approve`),
  accountConfig: () => request<{ accounts: boolean }>('GET', '/api/account/config'),
  requestSignup: (email: string) => request<void>('POST', '/api/account/signup', { email }),
  completeSignup: (token: string, username: string, password: string) =>
    request<User>('POST', '/api/account/signup/complete', { token, username, password }),
  requestReset: (email: string) => request<void>('POST', '/api/account/reset', { email }),
  completeReset: (token: string, password: string) =>
    request<void>('POST', '/api/account/reset/complete', { token, password }),
  requestLink: (email: string, password: string) => request<void>('POST', '/api/account/email', { email, password }),
  completeLink: (token: string) => request<User>('POST', '/api/account/email/complete', { token }),
  audit: () => request<AuditEntry[]>('GET', '/api/audit'),
};
