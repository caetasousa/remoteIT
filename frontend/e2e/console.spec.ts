import { expect, test, type Page } from '@playwright/test';

const ADMIN = { user: 'admin', pass: process.env.E2E_ADMIN_PASS ?? 'e2e-admin-pass' };
const AGENT_HOST = 'e2e-pc'; // hostname do container do agente no compose.e2e.yaml

async function login(page: Page, user: string, pass: string) {
  await page.goto('/');
  await page.getByLabel('Usuário').fill(user);
  await page.getByLabel('Senha').fill(pass);
  await page.getByRole('button', { name: 'Entrar' }).click();
}

async function logout(page: Page) {
  await page.getByRole('button', { name: 'Sair' }).click();
  await expect(page.getByRole('button', { name: 'Entrar' })).toBeVisible();
}

/** Conecta na máquina de teste e espera a imagem aparecer no canvas. */
async function viewAgent(page: Page) {
  const row = page.getByTestId('agent-row').filter({ hasText: AGENT_HOST });
  await expect(row).toBeVisible({ timeout: 20_000 });
  await row.getByRole('button', { name: /Conectar|Assumir sessão/ }).click();
  await expect(page.getByTestId('viewer-status')).toHaveText('conectado', { timeout: 10_000 });

  // A tela sintética do agente é um gradiente 640x360: pixels não pretos
  // provam que quadros atravessaram agente -> relay -> navegador.
  await expect
    .poll(
      () =>
        page.getByTestId('screen').evaluate((c: HTMLCanvasElement) => {
          if (c.width !== 640) return false;
          const d = c.getContext('2d')!.getImageData(0, 0, c.width, c.height).data;
          for (let i = 0; i < d.length; i += 4 * 101) if (d[i] + d[i + 1] + d[i + 2] > 60) return true;
          return false;
        }),
      { timeout: 10_000 },
    )
    .toBe(true);
}

test('senha errada mostra erro e não entra', async ({ page }) => {
  await login(page, ADMIN.user, 'senha-errada');
  await expect(page.getByRole('alert')).toHaveText('usuário ou senha inválidos');
  await expect(page.getByTestId('current-user')).toHaveCount(0);
});

test('admin vê a tela remota da máquina', async ({ page }) => {
  await login(page, ADMIN.user, ADMIN.pass);
  await viewAgent(page);
  await expect(page.locator('.stats')).toContainText('JPEG', { timeout: 5_000 });
  await page.getByRole('button', { name: '← Voltar' }).click();
  await expect(page.getByRole('button', { name: 'Máquinas' })).toBeVisible();
});

test('técnico cadastrado acessa a máquina e o acesso fica na auditoria', async ({ page }) => {
  const tech = { user: `tec${Date.now() % 1_000_000}`, pass: 'senha-do-tecnico' };

  await login(page, ADMIN.user, ADMIN.pass);
  await page.getByRole('button', { name: 'Usuários' }).click();
  await page.getByPlaceholder('usuário').fill(tech.user);
  await page.getByPlaceholder('senha (mín. 8)').fill(tech.pass);
  await page.getByRole('button', { name: 'Cadastrar' }).click();
  await expect(page.getByTestId('user-row').filter({ hasText: tech.user })).toBeVisible();
  await logout(page);

  await login(page, tech.user, tech.pass);
  await expect(page.getByTestId('current-user')).toHaveText(tech.user);
  await expect(page.getByRole('button', { name: 'Usuários' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Auditoria' })).toHaveCount(0);
  await viewAgent(page);
  await page.getByRole('button', { name: '← Voltar' }).click();
  await logout(page);

  await login(page, ADMIN.user, ADMIN.pass);
  await page.getByRole('button', { name: 'Auditoria' }).click();
  const entry = page.getByTestId('audit-row').filter({ hasText: tech.user });
  await expect(entry.first()).toContainText(AGENT_HOST);
  await expect(entry.first()).not.toContainText('em andamento');
});
