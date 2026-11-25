// Sessão de visualização: WebSocket com o relay, decodificação e desenho no
// canvas. É TypeScript puro de propósito: o caminho de cada quadro não passa
// pela reatividade do Svelte, que só recebe status e estatísticas (1x/s).

import {
  CODEC_AV1,
  CODEC_H264,
  CODEC_JPEG,
  CODEC_PNG,
  parseFrame,
  type ClientMessage,
  type CodecName,
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

type Accel = 'prefer-hardware' | 'no-preference';

/**
 * Como este navegador decodifica o codec: na GPU, só em software ou não
 * decodifica (null). No Chromium, 'prefer-hardware' recusa o decodificador
 * em software, então ele só serve quando a GPU decodifica de fato.
 */
async function probe(codec: string): Promise<Accel | null> {
  if (!('VideoDecoder' in window)) return null;
  for (const hardwareAcceleration of ['prefer-hardware', 'no-preference'] as const) {
    try {
      const r = await VideoDecoder.isConfigSupported({ codec, optimizeForLatency: true, hardwareAcceleration });
      if (r.supported) return hardwareAcceleration;
    } catch {
      // configuração inválida neste navegador: tenta a próxima
    }
  }
  return null;
}

/** H.264 na GPU ou, sem ela (máquina virtual, por exemplo), em software. */
export const probeH264 = () => probe('avc1.640033');

/**
 * AV1 só com decodificação na GPU: em software, 4K a 60 fps pesa demais no PC
 * do técnico, e o H.264 por hardware sai melhor.
 */
export async function probeAV1(): Promise<Accel | null> {
  return (await probe(av1Codec(1920, 1080))) === 'prefer-hardware' ? 'prefer-hardware' : null;
}

/**
 * Codec string AV1 (perfil Main, 8 bits). O nível sai do tamanho: 4.0 até
 * 1080p e 5.1 acima (4K a 60 fps). Os decodificadores olham o fluxo de
 * verdade; a string só precisa ser aceita.
 */
export function av1Codec(w: number, h: number): string {
  return w * h > 2048 * 1152 ? 'av01.0.13M.08' : 'av01.0.08M.08';
}

/**
 * Lista do hello: AV1 primeiro (menos banda com texto), depois H.264; JPEG
 * fecha a lista. O 4:4:4 (avc444) fica de fora: o WebCodecs não recompõe as
 * duas visões; isso é coisa do visualizador nativo.
 */
export function codecList(h264: boolean, av1: boolean, broken: ReadonlySet<CodecName> = new Set()): CodecName[] {
  const out: CodecName[] = [];
  if (av1) out.push('av1');
  if (h264) out.push('h264');
  return [...out.filter((c) => !broken.has(c)), 'jpeg'];
}

/** Falhas seguidas que tiram um codec do hello (uma só reinicia o decodificador). */
const MAX_DECODE_ERRORS = 3;
/** Quadros entregues ao decodificador AV1 sem sair imagem nenhuma: ele travou. */
const MAX_SILENT_CHUNKS = 120;

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
  private can: { h264: Accel | null; av1: Accel | null } = { h264: null, av1: null };
  /** Codecs que falharam nesta sessão: saem do hello e o agente troca. */
  private readonly broken = new Set<CodecName>();
  private errors = 0;
  private silent = 0;
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
    const [h264, av1] = await Promise.all([probeH264(), probeAV1()]);
    this.can = { h264, av1 };
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/ws/viewer?id=${encodeURIComponent(agentId)}`);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      this.sendHello();
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

  private sendHello() {
    const codecs = codecList(this.can.h264 !== null, this.can.av1 !== null, this.broken);
    this.send({ t: 'hello', h264: codecs.includes('h264'), codecs });
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
    this.silent = 0;
  }

  /**
   * O decodificador falhou: recomeça de um keyframe. Na terceira falha
   * seguida, desiste do codec e manda um hello sem ele; o agente reabre a
   * captura com o próximo da lista (AV1 → H.264 → JPEG).
   */
  private decoderFailed(codec: CodecName) {
    this.resetDecoder();
    if (++this.errors >= MAX_DECODE_ERRORS && codec !== 'jpeg') {
      console.warn(`decodificador ${codec} falhou ${this.errors} vezes; pedindo outro codec`);
      this.broken.add(codec);
      this.errors = 0;
      this.sendHello();
      return;
    }
    this.requestKeyframe();
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
    if (header.codec !== CODEC_H264 && header.codec !== CODEC_AV1) {
      this.send({ t: 'ack', s: header.seq });
      return;
    }
    const name: CodecName = header.codec === CODEC_AV1 ? 'av1' : 'h264';

    if (header.keyframe) {
      const codec = name === 'av1' ? av1Codec(header.w, header.h) : (codecFromSPS(data) ?? 'avc1.640033');
      const key = `${codec}/${header.w}x${header.h}`;
      if (!this.dec || this.dec.state === 'closed' || key !== this.decKey) {
        this.resetDecoder();
        this.dec = new VideoDecoder({
          output: (f) => {
            this.silent = 0;
            this.errors = 0;
            this.draw(f, f.displayWidth, f.displayHeight);
            f.close();
          },
          error: (e) => {
            console.warn('decoder:', e);
            this.decoderFailed(name);
          },
        });
        const hardwareAcceleration = this.can[name] ?? 'no-preference';
        this.dec.configure({ codec, optimizeForLatency: true, hardwareAcceleration });
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
      this.decoderFailed(name);
      return;
    }
    // AV1 vem de encoders de GPU recentes, ainda pouco testados com o
    // WebCodecs: se o decodificador engole quadros sem devolver imagem,
    // conta como falha (H.264 tem anos de estrada e não precisa disso)
    if (name === 'av1' && ++this.silent > MAX_SILENT_CHUNKS) this.decoderFailed(name);
  }
}
