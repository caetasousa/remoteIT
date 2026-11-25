import { expect, test, type Page } from '@playwright/test';

const MAILPIT = process.env.E2E_MAILPIT_URL ?? 'http://localhost:8025';
const ADMIN_PASS = process.env.E2E_ADMIN_PASS ?? 'e2e-admin-pass';

async function login(page: Page, user: string, pass: string) {
  await page.goto('/');
  await page.getByLabel('Usuário ou e-mail').fill(user);
  await page.getByLabel('Senha').fill(pass);
  await page.getByRole('button', { name: 'Entrar' }).click();
}

async function logout(page: Page) {
  await page.getByRole('button', { name: 'Sair' }).click();
  await expect(page.getByRole('button', { name: 'Entrar' })).toBeVisible();
}

/** Espera no Mailpit o link da rota pedida. */
async function mailLink(page: Page, to: string, route: string): Promise<string> {
  let link = '';
  await expect
    .poll(
      async () => {
        const res = await page.request.get(`${MAILPIT}/api/v1/messages?limit=200`);
        const { messages } = (await res.json()) as { messages: { ID: string; To: { Address: string }[] }[] };
        for (const m of messages.filter((m) => m.To[0]?.Address === to)) {
          const { Text } = await (await page.request.get(`${MAILPIT}/api/v1/message/${m.ID}`)).json();
          link = new RegExp(`http\\S+#/${route}\\?token=[\\w-]+`).exec(Text)?.[0] ?? '';
          if (link) return true;
        }
        return false;
      },
      { timeout: 15_000 },
    )
    .toBe(true);
  return link;
}

async function setPassword(page: Page, password: string) {
  await page.getByLabel('Senha (mín. 8)').fill(password);
  await page.getByLabel('Repita a senha').fill(password);
  await page.getByRole('button', { name: 'Salvar' }).click();
}

test('cria conta, espera aprovação e recupera o acesso', async ({ page }) => {
  const user = `nova${Date.now() % 1_000_000}`;
  const email = `${user}@example.test`;

  await page.goto('/');
  await page.getByRole('button', { name: 'Criar conta' }).click();
  await page.getByLabel('E-mail').fill(email);
  await page.getByRole('button', { name: 'Enviar link' }).click();
  await expect(page.getByRole('status')).toContainText('vai receber um link');

  // a aba já está com o console aberto: só o hash muda
  await page.goto(await mailLink(page, email, 'criar-conta'));
  await expect(page.getByRole('heading', { name: 'Concluir cadastro' })).toBeVisible();
  expect(page.url()).not.toContain('token');
  await page.getByLabel('Nome de usuário').fill(user);
  await setPassword(page, 'primeira-senha');
  await expect(page.getByRole('status')).toContainText('Aguarde a aprovação');

  await login(page, user, 'primeira-senha');
  await expect(page.getByRole('alert')).toContainText('aguardando aprovação');

  await login(page, 'admin', ADMIN_PASS);
  await page.getByRole('button', { name: 'Usuários' }).click();
  const row = page.getByTestId('user-row').filter({ hasText: email });
  await expect(row).toContainText('Aguardando aprovação');
  await row.getByRole('button', { name: 'Aprovar' }).click();
  // a confirmação é um modal do console, não o confirm() do navegador
  const modal = page.getByRole('dialog');
  await expect(modal).toContainText(`Aprovar ${user}?`);
  await modal.getByRole('button', { name: 'Aprovar' }).click();
  await expect(modal).toBeHidden();
  await expect(row).toContainText('Ativo');
  await logout(page);

  await login(page, email, 'primeira-senha');
  await expect(page.getByTestId('current-user')).toHaveText(user);
  await logout(page);

  await page.getByRole('button', { name: 'Esqueci meu acesso' }).click();
  await page.getByLabel('E-mail').fill(email);
  await page.getByRole('button', { name: 'Enviar link' }).click();
  await page.goto(await mailLink(page, email, 'nova-senha'));
  await setPassword(page, 'segunda-senha');
  await expect(page.getByRole('status')).toContainText('Senha alterada');
  await page.getByRole('button', { name: 'Ir para o login' }).click();

  await login(page, user, 'primeira-senha');
  await expect(page.getByRole('alert')).toHaveText('usuário ou senha inválidos');
  await login(page, user, 'segunda-senha');
  await expect(page.getByTestId('current-user')).toHaveText(user);
});

test('conta antiga associa e-mail, mesmo abrindo o link deslogada', async ({ page }) => {
  const user = `antiga${Date.now() % 1_000_000}`;
  const email = `${user}@example.test`;
  const pass = 'senha-da-conta';

  await login(page, 'admin', ADMIN_PASS);
  await page.getByRole('button', { name: 'Usuários' }).click();
  await page.getByPlaceholder('usuário').fill(user);
  await page.getByPlaceholder('senha (mín. 8)').fill(pass);
  await page.getByRole('button', { name: 'Cadastrar' }).click();
  await expect(page.getByTestId('user-row').filter({ hasText: user })).toBeVisible();
  await logout(page);

  await login(page, user, pass);
  await page.getByRole('button', { name: 'Adicionar e-mail' }).click();
  await page.getByPlaceholder('seu e-mail').fill(email);
  await page.getByPlaceholder('senha atual').fill(pass);
  await page.getByRole('button', { name: 'Enviar confirmação' }).click();
  const link = await mailLink(page, email, 'confirmar-email');
  await logout(page);

  await page.goto(link);
  await expect(page.getByRole('status')).toContainText('Entre na sua conta');
  await page.getByLabel('Usuário ou e-mail').fill(user);
  await page.getByLabel('Senha').fill(pass);
  await page.getByRole('button', { name: 'Entrar' }).click();
  await page.getByRole('button', { name: 'Confirmar' }).click();
  await expect(page.getByTestId('current-user')).toHaveText(user);
  await expect(page.getByRole('button', { name: 'Adicionar e-mail' })).toHaveCount(0);
});
