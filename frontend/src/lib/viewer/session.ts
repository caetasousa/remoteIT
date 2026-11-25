// Sessão de visualização: WebSocket com o relay, decodificação e desenho no
// canvas. É TypeScript puro de propósito: o caminho de cada quadro não passa
// pela reatividade do Svelte, que só recebe status e estatísticas (1x/s).

import {
  CODEC_H264,
  CODEC_JPEG,
  CODEC_PNG,
  parseFrame,
  type ClientMessage,
  type Preset,
  type ServerMessage,
} from '../protocol';

export type StatusLevel = '' | 'ok' | 'warn' | 'bad';

/** Telemetria de um quadro, já pronta para exibição. */
export interface Stats {
  enc: string;
  res: string;
  fps: number;
  mbps: string;
  rtt: number;
}

export interface SessionEvents {
  status(text: string, level: StatusLevel): void;
  stats(s: Stats | null): void;
  /** Chamado quando o tamanho do quadro muda, para re-ajustar a exibição. */
  resized?(): void;
  cursorShape(css: string): void;
  /** Posição do cursor remoto em pixels do quadro; visible=false esconde. */
  cursorPos(x: number, y: number, visible: boolean): void;
}

const STATUS: Record<string, [string, StatusLevel]> = {
  waiting: ['aguardando o usuário aprovar…', 'warn'],
  denied: ['acesso RECUSADO pelo usuário', 'bad'],
  granted: ['conectado', 'ok'],
  secure: ['tela protegida (UAC / bloqueio) — aguardando…', 'warn'],
};

export async function canDecodeH264(): Promise<boolean> {
  if (!('VideoDecoder' in window)) return false;
  try {
    const r = await VideoDecoder.isConfigSupported({ codec: 'avc1.640033', optimizeForLatency: true });
    return r.supported === true;
  } catch {
    return false;
  }
}

/** Monta a codec string exata a partir do SPS (perfil/nível reais do encoder). */
export function codecFromSPS(u: Uint8Array): string | null {
  for (let i = 0; i + 4 < u.length; i++) {
    if (u[i] === 0 && u[i + 1] === 0 && (u[i + 2] === 1 || (u[i + 2] === 0 && u[i + 3] === 1))) {
      const j = i + (u[i + 2] === 1 ? 3 : 4);
      if ((u[j] & 0x1f) === 7) {
        return 'avc1.' + [u[j + 1], u[j + 2], u[j + 3]].map((b) => b.toString(16).padStart(2, '0')).join('');
      }
    }
  }
  return null;
}

export class ViewerSession {
  private ws: WebSocket | null = null;
  private dec: VideoDecoder | null = null;
  private decKey = '';
  private needKey = true;
  private lastKf = 0;
  private rendered = 0;
  /** Conta os quadros de vídeo recebidos; o refresh PNG só vale se nenhum chegou depois dele. */
  private videoGen = 0;
  private readonly ctx: CanvasRenderingContext2D;

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly events: SessionEvents,
  ) {
    this.ctx = canvas.getContext('2d', { alpha: false, desynchronized: true })!;
  }

  async connect(agentId: string, preset: Preset) {
    this.events.status('conectando…', '');
    this.ctx.fillStyle = '#000';
    this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
    const h264 = await canDecodeH264();
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/ws/viewer?id=${encodeURIComponent(agentId)}`);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      this.send({ t: 'hello', h264 });
      this.send({ t: 'cfg', p: preset });
    };
    ws.onmessage = (e) =>
      typeof e.data === 'string' ? this.onText(JSON.parse(e.data) as ServerMessage) : this.onFrame(e.data);
    ws.onclose = () => {
      if (this.ws === ws) this.events.status('desconectado', 'bad');
      this.resetDecoder();
    };
    this.ws = ws;
  }

  send(m: ClientMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  close() {
    const ws = this.ws;
    this.ws = null;
    ws?.close();
    this.resetDecoder();
  }

  private onText(m: ServerMessage) {
    switch (m.t) {
      case 'stats': {
        const fps = this.rendered;
        this.rendered = 0;
        this.events.stats({
          enc: m.enc,
          res: `${m.w}×${m.h}`,
          fps,
          mbps: (m.kbps / 1000).toFixed(1),
          rtt: m.rtt,
        });
        return;
      }
      case 'cs':
        this.events.cursorShape(m.c);
        return;
      case 'cur':
        this.events.cursorPos(m.x, m.y, m.v);
        return;
      default: {
        const s = STATUS[m.t];
        if (s) this.events.status(...s);
      }
    }
  }

  private resetDecoder() {
    if (this.dec && this.dec.state !== 'closed') this.dec.close();
    this.dec = null;
    this.decKey = '';
    this.needKey = true;
  }

  private requestKeyframe() {
    const n = performance.now();
    if (n - this.lastKf > 400) {
      this.lastKf = n;
      this.send({ t: 'kf' });
    }
  }

  private draw(src: CanvasImageSource, w: number, h: number) {
    if (this.canvas.width !== w || this.canvas.height !== h) {
      this.canvas.width = w;
      this.canvas.height = h;
      this.events.resized?.();
    }
    this.ctx.drawImage(src, 0, 0, w, h);
    this.rendered++;
  }

  /**
   * Refresh de nitidez: imagem PNG sem perda que o agente manda quando a tela
   * para (veja internal/agent/media/still.go). Corrige a cor em meia
   * resolução do H.264 (texto colorido borrado). Desenha por cima do último
   * quadro, sem trocar o tamanho do canvas e sem contar como FPS — e só se
   * nenhum quadro de vídeo chegou enquanto a imagem era decodificada, senão
   * cobriria a tela nova com uma velha.
   */
  private onStill(data: Uint8Array<ArrayBuffer>) {
    const gen = this.videoGen;
    createImageBitmap(new Blob([data], { type: 'image/png' }))
      .then((b) => {
        if (gen === this.videoGen && b.width === this.canvas.width && b.height === this.canvas.height) {
          this.ctx.drawImage(b, 0, 0);
        }
        b.close();
      })
      .catch(() => {});
  }

  private onFrame(buf: ArrayBuffer) {
    const { header, data } = parseFrame(buf);
    if (header.codec === CODEC_PNG) {
      this.send({ t: 'ack', s: header.seq });
      this.onStill(data);
      return;
    }
    this.videoGen++;
    if (header.codec === CODEC_JPEG) {
      this.send({ t: 'ack', s: header.seq });
      createImageBitmap(new Blob([data], { type: 'image/jpeg' }))
        .then((b) => {
          this.draw(b, b.width, b.height);
          b.close();
        })
        .catch(() => {});
      return;
    }
    if (header.codec !== CODEC_H264) return;

    if (header.keyframe) {
      const codec = codecFromSPS(data) ?? 'avc1.640033';
      const key = `${codec}/${header.w}x${header.h}`;
      if (!this.dec || this.dec.state === 'closed' || key !== this.decKey) {
        this.resetDecoder();
        this.dec = new VideoDecoder({
          output: (f) => {
            this.draw(f, f.displayWidth, f.displayHeight);
            f.close();
          },
          error: (e) => {
            console.warn('decoder:', e);
            this.resetDecoder();
            this.requestKeyframe();
          },
        });
        this.dec.configure({ codec, optimizeForLatency: true, hardwareAcceleration: 'prefer-hardware' });
        this.decKey = key;
      }
      this.needKey = false;
    } else if (this.needKey || !this.dec) {
      this.send({ t: 'ack', s: header.seq });
      this.requestKeyframe();
      return;
    }
    // ACK imediato: libera o pipeline do agente sem esperar o decoder (~5-15 ms)
    this.send({ t: 'ack', s: header.seq });
    try {
      this.dec!.decode(new EncodedVideoChunk({ type: header.keyframe ? 'key' : 'delta', timestamp: header.seq, data }));
    } catch (e) {
      console.warn(e);
      this.resetDecoder();
      this.requestKeyframe();
    }
  }
}
