import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import ts from 'typescript';

// O desenho do console (src/lib/viewer/render.ts) num navegador de verdade,
// sem relay nem agente: o módulo é transpilado aqui e posto numa página
// vazia. A recomposição 4:4:4 em WebGL é comparada com a referência em Go
// (internal/avc444, que gera e/fixtures/avc444.json).

function renderModule(): string {
  const src = readFileSync(new URL('../src/lib/viewer/render.ts', import.meta.url), 'utf8');
  const js = ts.transpileModule(src, {
    compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  return js.replace(/^export /gm, '') + '\nwindow.__render = { createRenderer };\n';
}

test.beforeEach(async ({ page }) => {
  await page.setContent('<canvas id="c"></canvas>');
  await page.addScriptTag({ content: renderModule(), type: 'module' });
  await page.waitForFunction(() => 'createRenderer' in ((window as unknown as { __render?: object }).__render ?? {}));
});

test('WebGL recompõe a cor 4:4:4 como a referência em Go', async ({ page }) => {
  const fx = JSON.parse(readFileSync(new URL('./fixtures/avc444.json', import.meta.url), 'utf8'));
  const res = await page.evaluate((fx) => {
    const b = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
    const plane = (p: { y: string; u: string; v: string }) => ({ w: fx.w, h: fx.h, y: b(p.y), u: b(p.u), v: b(p.v) });
    const c = document.getElementById('c') as HTMLCanvasElement;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const r = (window as any).__render.createRenderer(c);
    if (!r.gl) return { gl: false };
    // lê o canvas WebGL por uma cópia 2D (o canvas guarda a imagem: preserveDrawingBuffer)
    const pixels = () => {
      const t = document.createElement('canvas');
      t.width = c.width;
      t.height = c.height;
      const x = t.getContext('2d')!;
      x.drawImage(c, 0, 0);
      return x.getImageData(0, 0, c.width, c.height).data;
    };
    const diff = (rgba: Uint8ClampedArray, bgra: Uint8Array) => {
      let worst = 0;
      for (let i = 0; i < bgra.length; i += 4)
        for (const [g, w] of [
          [rgba[i], bgra[i + 2]],
          [rgba[i + 1], bgra[i + 1]],
          [rgba[i + 2], bgra[i]],
        ])
          worst = Math.max(worst, Math.abs(g - w));
      return worst;
    };
    r.size(fx.w, fx.h);
    r.yuv444(plane(fx.main), plane(fx.aux), null);
    const d444 = diff(pixels(), b(fx.bgra444));
    r.yuv444(plane(fx.main), null, null);
    const dMain = diff(pixels(), b(fx.bgraMain));
    return { gl: true, d444, dMain };
  }, fx);
  expect(res.gl, 'o Chromium do teste deveria ter WebGL2').toBe(true);
  expect(res.d444).toBeLessThanOrEqual(1);
  expect(res.dMain).toBeLessThanOrEqual(1);
});

test('o vídeo só pinta a região, e o retalho sem perda fica', async ({ page }) => {
  const out = await page.evaluate(async () => {
    const c = document.getElementById('c') as HTMLCanvasElement;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const r = (window as any).__render.createRenderer(c);
    const solid = (w: number, h: number, rgb: number[]) => {
      const d = new ImageData(w, h);
      for (let i = 0; i < d.data.length; i += 4) d.data.set([...rgb, 255], i);
      return createImageBitmap(d);
    };
    r.size(8, 4);
    r.image(await solid(8, 4, [200, 0, 0]), 8, 4, null); // quadro inteiro vermelho
    r.patch(await solid(4, 4, [0, 0, 200]), 4, 0); // retalho azul na metade direita
    r.image(await solid(8, 4, [0, 200, 0]), 8, 4, [{ x: 0, y: 0, w: 4, h: 4 }]); // vídeo verde só na esquerda
    r.image(await solid(8, 4, [9, 9, 9]), 8, 4, []); // região vazia: nada muda
    const t = document.createElement('canvas');
    t.width = 8;
    t.height = 4;
    const x = t.getContext('2d')!;
    x.drawImage(c, 0, 0);
    const px = (i: number, j: number) => Array.from(x.getImageData(i, j, 1, 1).data.slice(0, 3));
    return { gl: r.gl, left: px(1, 2), right: px(6, 1) };
  });
  expect(out.gl).toBe(true);
  expect(out.left).toEqual([0, 200, 0]);
  expect(out.right).toEqual([0, 0, 200]);
});
