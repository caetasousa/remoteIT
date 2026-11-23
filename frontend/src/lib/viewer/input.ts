// Captura mouse e teclado sobre o canvas e envia ao agente. Devolve a função
// que remove os listeners.

import type { ClientMessage } from '../protocol';

export function attachInput(canvas: HTMLCanvasElement, send: (m: ClientMessage) => void): () => void {
  const pos = (e: PointerEvent) => {
    const r = canvas.getBoundingClientRect();
    return {
      x: Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)),
      y: Math.min(1, Math.max(0, (e.clientY - r.top) / r.height)),
    };
  };
  const down = new Set<string>();
  let wheelAcc = 0;

  const handlers: [keyof HTMLElementEventMap, (e: any) => void, AddEventListenerOptions?][] = [
    ['pointermove', (e: PointerEvent) => send({ t: 'mm', ...pos(e) })],
    [
      'pointerdown',
      (e: PointerEvent) => {
        canvas.focus();
        canvas.setPointerCapture(e.pointerId);
        e.preventDefault();
        send({ t: 'md', b: e.button, ...pos(e) });
      },
    ],
    [
      'pointerup',
      (e: PointerEvent) => {
        e.preventDefault();
        send({ t: 'mu', b: e.button, ...pos(e) });
      },
    ],
    ['contextmenu', (e: Event) => e.preventDefault()],
    [
      'wheel',
      (e: WheelEvent) => {
        e.preventDefault();
        wheelAcc += e.deltaMode === 1 ? e.deltaY * 40 : e.deltaY; // touchpad: acumula deltas pequenos
        const steps = Math.trunc(wheelAcc / 100);
        if (steps) {
          send({ t: 'wh', d: steps * 120 });
          wheelAcc -= steps * 100;
        }
      },
      { passive: false },
    ],
    [
      'keydown',
      (e: KeyboardEvent) => {
        e.preventDefault();
        down.add(e.code);
        send({ t: 'kd', code: e.code, key: e.key });
      },
    ],
    [
      'keyup',
      (e: KeyboardEvent) => {
        e.preventDefault();
        down.delete(e.code);
        send({ t: 'ku', code: e.code, key: e.key });
      },
    ],
    [
      // ao perder o foco, solta as teclas presas (evita Ctrl "grudado" no PC remoto)
      'blur',
      () => {
        for (const code of down) send({ t: 'ku', code, key: '' });
        down.clear();
      },
    ],
  ];

  for (const [type, fn, opts] of handlers) canvas.addEventListener(type, fn, opts);
  return () => {
    for (const [type, fn, opts] of handlers) canvas.removeEventListener(type, fn, opts);
  };
}

/** Envia Ctrl+Shift+Esc (abre o Gerenciador de Tarefas no PC remoto). */
export function sendCtrlShiftEsc(send: (m: ClientMessage) => void) {
  for (const code of ['ControlLeft', 'ShiftLeft', 'Escape']) send({ t: 'kd', code, key: '' });
  for (const code of ['Escape', 'ShiftLeft', 'ControlLeft']) send({ t: 'ku', code, key: '' });
}

/** Pede para bloquear a tela do PC remoto (equivalente a Win+L). */
export function sendLock(send: (m: ClientMessage) => void) {
  send({ t: 'lock' });
}
