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

// 1,25 = Windows com escala de 125%, onde 1 CSS px não é 1 pixel do monitor
test.describe('monitor em 125%', () => {
  test.use({ deviceScaleFactor: 1.25 });

  test('tamanho real mostra um pixel remoto por pixel do monitor e é lembrado', async ({ page }) => {
    await login(page, ADMIN.user, ADMIN.pass);
    await viewAgent(page);
    const screen = page.getByTestId('screen');
    // largura exibida em pixels físicos do monitor
    const shownPx = () =>
      screen.evaluate((c: HTMLCanvasElement) => Math.round(c.getBoundingClientRect().width * devicePixelRatio));

    // ajustado à janela, a tela sintética de 640 px é ampliada
    await expect.poll(shownPx).toBeGreaterThan(640);

    const zoom = page.getByRole('button', { name: 'Tamanho real' });
    await zoom.click();
    await expect(zoom).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(shownPx).toBe(640);

    // a escolha vale para a próxima sessão
    await page.getByRole('button', { name: '← Voltar' }).click();
    await viewAgent(page);
    await expect(page.getByRole('button', { name: 'Tamanho real' })).toHaveAttribute('aria-pressed', 'true');
    await expect.poll(shownPx).toBe(640);

    await page.getByRole('button', { name: 'Tamanho real' }).click();
    await expect.poll(shownPx).toBeGreaterThan(640);
  });
});

test('técnico não vê máquinas que não são dele e o acesso do admin fica na auditoria', async ({ page }) => {
  const tech = { user: `tec${Date.now() % 1_000_000}`, pass: 'senha-do-tecnico' };

  await login(page, ADMIN.user, ADMIN.pass);
  await page.getByRole('button', { name: 'Usuários' }).click();
  await page.getByPlaceholder('usuário').fill(tech.user);
  await page.getByPlaceholder('senha (mín. 8)').fill(tech.pass);
  await page.getByRole('button', { name: 'Cadastrar' }).click();
  await expect(page.getByTestId('user-row').filter({ hasText: tech.user })).toBeVisible();
  await logout(page);

  // a máquina de teste não é do técnico: ele não a vê nem tem como compartilhar
  await login(page, tech.user, tech.pass);
  await expect(page.getByTestId('current-user')).toHaveText(tech.user);
  await expect(page.getByRole('button', { name: 'Usuários' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Auditoria' })).toHaveCount(0);
  await expect(page.getByText('Nenhuma máquina ainda')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Compartilhar' })).toHaveCount(0);
  await logout(page);

  await login(page, ADMIN.user, ADMIN.pass);
  await viewAgent(page);
  await page.getByRole('button', { name: '← Voltar' }).click();
  await page.getByRole('button', { name: 'Auditoria' }).click();
  const entry = page.getByTestId('audit-row').filter({ hasText: ADMIN.user });
  await expect(entry.first()).toContainText(AGENT_HOST);
  await expect(entry.first()).not.toContainText('em andamento');
});

test('link de instalação baixa o agente sem login', async ({ page, request }) => {
  await login(page, ADMIN.user, ADMIN.pass);
  // os downloads pelo navegador vão em .zip (o .exe solto o navegador segura)
  for (const [name, path] of [
    ['Executar agente (.zip)', '/download/agent.zip'],
    ['Instalar como serviço (.zip)', '/download/agent-servico.zip'],
    ['Visualizador do técnico (.zip)', '/download/viewer.zip'],
  ]) {
    await expect(page.getByRole('link', { name })).toHaveAttribute('href', new RegExp(`^${path}\\?t=\\d+$`));
  }
  await page.getByLabel('Edição').selectOption('servico');
  await page.getByLabel('Validade').selectOption('0'); // sem prazo
  await page.getByLabel('Máquinas').selectOption('0'); // sem limite
  await page.getByRole('button', { name: 'Gerar link' }).click();
  await expect(page.locator('.links')).toContainText('sem prazo');
  const url = await page.getByLabel('Link gerado').inputValue();
  expect(url).toMatch(/\/i\/[a-z2-7]{32}$/);

  // quem recebe o link não tem conta: baixa direto
  const res = await request.get(new URL(url).pathname);
  // o compose de E2E não monta os .exe: 503 prova que o link foi aceito e o
  // relay foi buscar o agente; um link inválido daria 404
  expect([200, 503]).toContain(res.status());
  const bad = await request.get('/i/nao-existe');
  expect(bad.status()).toBe(404);

  // o mesmo link, sem navegador: o comando do PowerShell aponta para o script
  await expect(page.getByLabel('Comando do PowerShell')).toHaveValue(`irm ${url}/ps | iex`);
  const ps = await request.get(new URL(url).pathname + '/ps');
  expect([200, 503]).toContain(ps.status());
  expect((await request.get('/i/nao-existe/ps')).status()).toBe(404);

  // revogar pede confirmação num modal do console: Esc cancela, o botão revoga
  const items = page.locator('.links li');
  const before = await items.count();
  await items.first().getByRole('button', { name: 'revogar' }).click();
  const modal = page.getByRole('dialog');
  await expect(modal).toContainText('Revogar este link?');
  await page.keyboard.press('Escape');
  await expect(modal).toBeHidden();
  await expect(items).toHaveCount(before);
  await items.first().getByRole('button', { name: 'revogar' }).click();
  await modal.getByRole('button', { name: 'Revogar' }).click();
  await expect(modal).toBeHidden();
  await expect(items).toHaveCount(before - 1);
  // o link revogado não baixa mais
  expect((await request.get(new URL(url).pathname)).status()).toBe(404);
});
