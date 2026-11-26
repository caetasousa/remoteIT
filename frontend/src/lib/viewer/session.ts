// Sessão de visualização: WebSocket com o relay, decodificação e desenho no
// canvas. É TypeScript puro de propósito: o caminho de cada quadro não passa
// pela reatividade do Svelte, que só recebe status e estatísticas (1x/s).

import {
  CODEC_AV1,
  CODEC_AVC444,
  CODEC_H264,
  CODEC_JPEG,
  CODEC_PNG,
  CODEC_PNG_RECT,
  parseAVC444,
  parseFrame,
  parsePNGRects,
  type ClientMessage,
  type CodecName,
  type Preset,
  type ServerMessage,
} from '../protocol';
import { createRenderer, type I420, type Rect, type Renderer } from './render';

export type StatusLevel = '' | 'ok' | 'warn' | 'bad';

/** Telemetria de um quadro, já pronta para exibição. */
export interface Stats {
  enc: string;
  res: string;
  fps: number;
  mbps: string;
  rtt: number;
  /** quanto os quadros esperam em fila no caminho (ms); null em agentes antigos */
  queue: number | null;
  /** captura + codificação no PC remoto (ms) */
  encMs: number | null;
  /** da chegada ao desenho aqui (ms) */
  decMs: number | null;
  /** local = o console foi aberto pelo endereço do próprio PC do relay */
  route: 'local' | 'internet';
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
 * O 4:4:4 decodifica as duas visões H.264 em software (os quadros ficam na
 * memória, onde dá para ler os planos crus) e recompõe a cor em WebGL.
 */
export async function probe444(): Promise<boolean> {
  if (!('VideoDecoder' in window)) return false;
  try {
    const r = await VideoDecoder.isConfigSupported({
      codec: 'avc1.640033',
      optimizeForLatency: true,
      hardwareAcceleration: 'prefer-software',
    });
    return !!r.supported;
  } catch {
    return false;
  }
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
 * Lista do hello, em ordem de preferência: 4:4:4 (texto colorido nítido
 * também em movimento), AV1 (menos banda), H.264; JPEG fecha a lista.
 */
export function codecList(
  h264: boolean,
  av1: boolean,
  broken: ReadonlySet<CodecName> = new Set(),
  color444 = false,
): CodecName[] {
  const out: CodecName[] = [];
  if (color444 && h264) out.push('avc444');
  if (av1) out.push('av1');
  if (h264) out.push('h264');
  return [...out.filter((c) => !broken.has(c)), 'jpeg'];
}

/** Falhas seguidas que tiram um codec do hello (uma só reinicia o decodificador). */
const MAX_DECODE_ERRORS = 3;
/** Quadros entregues ao decodificador AV1 sem sair imagem nenhuma: ele travou. */
const MAX_SILENT_CHUNKS = 120;
/** Fila do decodificador em software do 4:4:4 acima disso por tanto tempo: este PC não dá conta. */
const SLOW_QUEUE = 4;
const SLOW_FOR_MS = 1500;

/**
 * BT.709 faixa limitada: o que o agente grava no H.264/AV1. Vai explícito no
 * configure porque o encoder pode não gravar esses metadados no fluxo, e sem
 * eles o navegador pode supor BT.601 (cores levemente trocadas).
 */
const BT709: VideoColorSpaceInit = { primaries: 'bt709', transfer: 'bt709', matrix: 'bt709', fullRange: false };

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

/** Une as regiões: null (quadro inteiro) ganha de tudo. */
function union(a: Rect[] | null, b: Rect[] | null): Rect[] | null {
  return a === null || b === null ? null : [...a, ...b];
}

/**
 * Item da fila de desenho. Tudo o que pinta o canvas entra aqui na ordem de
 * chegada e sai na mesma ordem, quando fica pronto: um retalho sem perda
 * (decodificado em paralelo) não pode ser coberto por um quadro de vídeo que
 * veio antes dele, nem cobrir um que veio depois.
 */
interface Op {
  seq: number;
  at: number; // chegada (performance.now), para o tempo de decodificação
  ready: boolean;
  /** região do vídeo (null = inteiro); só para vídeo */
  region: Rect[] | null;
  video: boolean;
  codec?: CodecName;
  draw?: () => void; // pinta (null = não saiu imagem; só passa a região adiante)
  /** AVC444: quais visões ainda faltam sair */
  wantMain?: boolean;
  wantAux?: boolean;
  main?: I420;
  aux?: I420;
}

/** Dois decodificadores em software, um por visão do AVC444. */
interface Dec444 {
  main: VideoDecoder;
  aux: VideoDecoder;
  key: string;
}

export class ViewerSession {
  private ws: WebSocket | null = null;
  private dec: VideoDecoder | null = null;
  private decKey = '';
  private d444: Dec444 | null = null;
  private needKey = true;
  private lastKf = 0;
  private rendered = 0;
  private can: { h264: Accel | null; av1: Accel | null; sw444: boolean } = { h264: null, av1: null, sw444: false };
  /** Codecs que falharam nesta sessão: saem do hello e o agente troca. */
  private readonly broken = new Set<CodecName>();
  private errors = 0;
  private silent = 0;
  private slowSince = 0;
  private readonly r: Renderer;
  private view: { p: Preset; vw: number; vh: number } = { p: 'quality', vw: 0, vh: 0 };
  private color444 = true;

  private readonly regions = new Map<number, Rect[]>();
  private readonly ops: Op[] = [];
  private readonly bySeq = new Map<number, Op>();
  private carry: Rect[] | null = []; // região de quadros que não saíram, somada ao próximo
  private lastMain: I420 | null = null;
  private lastAux: I420 | null = null;
  private decSum = 0;
  private decN = 0;

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly events: SessionEvents,
  ) {
    this.r = createRenderer(canvas);
  }

  /** O 4:4:4 só é oferecido com WebGL2 e decodificação H.264 em software. */
  get supports444() {
    return this.r.gl && this.can.sw444;
  }

  async connect(agentId: string, preset: Preset, vw = 0, vh = 0, color444 = true) {
    this.events.status('conectando…', '');
    this.view = { p: preset, vw, vh };
    this.color444 = color444;
    this.r.size(this.canvas.width || 1, this.canvas.height || 1);
    const [h264, av1, sw444] = await Promise.all([probeH264(), probeAV1(), probe444()]);
    this.can = { h264, av1, sw444 };
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${proto}://${location.host}/ws/viewer?id=${encodeURIComponent(agentId)}`);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      this.sendHello();
      this.sendView();
    };
    ws.onmessage = (e) =>
      typeof e.data === 'string' ? this.onText(JSON.parse(e.data) as ServerMessage) : this.onFrame(e.data);
    ws.onclose = () => {
      if (this.ws === ws) this.events.status('desconectado', 'bad');
      this.resetDecoders();
    };
    this.ws = ws;
  }

  private sendHello() {
    const codecs = codecList(this.can.h264 !== null, this.can.av1 !== null, this.broken, this.color444 && this.supports444);
    this.send({ t: 'hello', h264: codecs.includes('h264'), codecs });
  }

  private sendView() {
    this.send({ t: 'cfg', p: this.view.p, vw: this.view.vw, vh: this.view.vh });
  }

  /** Troca o preset de qualidade (mantém a área). */
  setPreset(p: Preset) {
    this.view.p = p;
    this.sendView();
  }

  /**
   * Informa a área onde a tela aparece, em pixels físicos (0 x 0 = nativa,
   * no "Tamanho real"): o agente manda o vídeo nesse tamanho, em vez de
   * pixels que o navegador teria de jogar fora reduzindo.
   */
  setViewport(vw: number, vh: number) {
    if (vw === this.view.vw && vh === this.view.vh) return;
    this.view.vw = vw;
    this.view.vh = vh;
    this.sendView();
  }

  /** Liga ou desliga o pedido de cor 4:4:4 (se este navegador suporta). */
  setColor444(on: boolean) {
    if (on === this.color444) return;
    this.color444 = on;
    this.sendHello();
  }

  send(m: ClientMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m));
  }

  close() {
    const ws = this.ws;
    this.ws = null;
    ws?.close();
    this.resetDecoders();
  }

  private onText(m: ServerMessage) {
    switch (m.t) {
      case 'stats': {
        const fps = this.rendered;
        this.rendered = 0;
        const decMs = this.decN ? this.decSum / this.decN : null;
        this.decSum = this.decN = 0;
        this.events.stats({
          enc: m.enc,
          res: `${m.w}×${m.h}`,
          fps,
          mbps: (m.kbps / 1000).toFixed(1),
          rtt: m.rtt,
          queue: m.rttmin ? Math.max(0, m.rtt - m.rttmin) : null,
          encMs: m.encus ? m.encus / 1000 : null,
          decMs,
          route: ['localhost', '127.0.0.1', '[::1]'].includes(location.hostname) ? 'local' : 'internet',
        });
        return;
      }
      case 'rg':
        if (this.regions.size > 256) this.regions.clear(); // de quadros que nunca chegaram
        this.regions.set(
          m.s,
          Array.from({ length: Math.floor(m.r.length / 4) }, (_, i) => ({
            x: m.r[4 * i],
            y: m.r[4 * i + 1],
            w: m.r[4 * i + 2],
            h: m.r[4 * i + 3],
          })),
        );
        return;
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

  // ---------- fila de desenho ----------

  private push(op: Op) {
    this.ops.push(op);
    this.bySeq.set(op.seq, op);
  }

  /** Aplica, em ordem, tudo o que já está pronto. */
  private flush() {
    while (this.ops.length && this.ops[0].ready) {
      const op = this.ops.shift()!;
      this.bySeq.delete(op.seq);
      if (op.video) {
        op.region = union(this.carry, op.region);
        this.carry = [];
        if (op.draw) {
          op.draw();
          this.rendered++;
          this.decSum += performance.now() - op.at;
          this.decN++;
        } else {
          this.carry = op.region; // não saiu imagem: a região vai junto com o próximo
        }
      } else {
        op.draw?.();
      }
    }
  }

  /** Quadros que o decodificador não vai mais entregar (reiniciou): saem da fila sem imagem. */
  private dropVideo(codec?: CodecName) {
    for (const op of this.ops) {
      if (op.video && !op.ready && (!codec || op.codec === codec)) {
        op.ready = true;
        op.draw = undefined;
      }
    }
    this.flush();
  }

  private resetDecoders() {
    if (this.dec && this.dec.state !== 'closed') this.dec.close();
    this.dec = null;
    this.decKey = '';
    if (this.d444) {
      for (const d of [this.d444.main, this.d444.aux]) if (d.state !== 'closed') d.close();
      this.d444 = null;
    }
    this.needKey = true;
    this.silent = 0;
    this.slowSince = 0;
    this.dropVideo();
  }

  /**
   * O decodificador falhou: recomeça de um keyframe. Na terceira falha
   * seguida, desiste do codec e manda um hello sem ele; o agente reabre a
   * captura com o próximo da lista (4:4:4 → AV1 → H.264 → JPEG).
   */
  private decoderFailed(codec: CodecName, giveUp = false) {
    this.resetDecoders();
    if ((giveUp || ++this.errors >= MAX_DECODE_ERRORS) && codec !== 'jpeg') {
      console.warn(`decodificador ${codec} falhou; pedindo outro codec`);
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

  /** Ajusta o canvas ao quadro; num tamanho novo, o quadro tem de vir inteiro. */
  private fit(w: number, h: number) {
    if (this.r.size(w, h)) this.events.resized?.();
  }

  // ---------- quadros ----------

  private onFrame(buf: ArrayBuffer) {
    const { header, data } = parseFrame(buf);
    const at = performance.now();
    // ACK imediato: libera o pipeline do agente sem esperar decodificar
    this.send({ t: 'ack', s: header.seq });

    if (header.codec === CODEC_PNG || header.codec === CODEC_PNG_RECT) {
      // refresh sem perda: a tela inteira (PNG) ou retalhos das partes que
      // pararam de mudar, por cima do que já está na tela
      const parts =
        header.codec === CODEC_PNG ? [{ x: 0, y: 0, png: data }] : parsePNGRects(data);
      const op: Op = { seq: header.seq, at, ready: false, region: null, video: false };
      this.push(op);
      Promise.all(parts.map((p) => createImageBitmap(new Blob([p.png], { type: 'image/png' })).then((b) => ({ ...p, b }))))
        .then((imgs) => {
          op.draw = () => {
            for (const { x, y, b } of imgs) {
              if (this.canvas.width === header.w && this.canvas.height === header.h) this.r.patch(b, x, y);
              b.close();
            }
          };
        })
        .catch(() => {})
        .finally(() => {
          op.ready = true;
          this.flush();
        });
      return;
    }

    if (header.codec === CODEC_JPEG) {
      const op: Op = { seq: header.seq, at, ready: false, region: null, video: true, codec: 'jpeg' };
      this.push(op);
      createImageBitmap(new Blob([data], { type: 'image/jpeg' }))
        .then((b) => {
          op.draw = () => {
            this.fit(b.width, b.height);
            this.r.image(b, b.width, b.height, null);
            b.close();
          };
        })
        .catch(() => {})
        .finally(() => {
          op.ready = true;
          this.flush();
        });
      return;
    }

    if (header.codec === CODEC_AVC444) {
      this.onAVC444(header.seq, header.w, header.h, header.keyframe, data, at);
      return;
    }
    if (header.codec !== CODEC_H264 && header.codec !== CODEC_AV1) return;
    const name: CodecName = header.codec === CODEC_AV1 ? 'av1' : 'h264';

    if (header.keyframe) {
      const codec = name === 'av1' ? av1Codec(header.w, header.h) : (codecFromSPS(data) ?? 'avc1.640033');
      const key = `${codec}/${header.w}x${header.h}`;
      if (!this.dec || this.dec.state === 'closed' || key !== this.decKey) {
        this.resetDecoders();
        this.dec = new VideoDecoder({
          output: (f) => this.onVideo(f),
          error: (e) => {
            console.warn('decoder:', e);
            this.decoderFailed(name);
          },
        });
        const hardwareAcceleration = this.can[name] ?? 'no-preference';
        this.dec.configure({ codec, optimizeForLatency: true, hardwareAcceleration, colorSpace: BT709 });
        this.decKey = key;
      }
      this.needKey = false;
    } else if (this.needKey || !this.dec) {
      this.requestKeyframe();
      return;
    }
    this.push({ seq: header.seq, at, ready: false, region: this.region(header.seq), video: true, codec: name });
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

  /** A região do quadro seq: o que veio na mensagem rg, ou null (inteiro). */
  private region(seq: number): Rect[] | null {
    const r = this.regions.get(seq);
    this.regions.delete(seq);
    return r ?? null;
  }

  private onVideo(f: VideoFrame) {
    this.silent = 0;
    this.errors = 0;
    const op = this.bySeq.get(f.timestamp);
    if (!op) {
      f.close();
      return;
    }
    // quadros que o decodificador pulou antes deste não vão sair
    for (const o of this.ops) {
      if (o === op) break;
      if (o.video && !o.ready && o.codec === op.codec) o.ready = true;
    }
    op.draw = () => {
      this.fit(f.displayWidth, f.displayHeight);
      this.r.image(f, f.displayWidth, f.displayHeight, op.region);
      f.close();
    };
    op.ready = true;
    this.flush();
  }

  // ---------- cor 4:4:4 (AVC444) ----------

  private onAVC444(seq: number, w: number, h: number, keyframe: boolean, data: Uint8Array<ArrayBuffer>, at: number) {
    const p = parseAVC444(data);
    if (!p || !this.supports444) return;
    if (p.mainKey || p.auxKey || keyframe) {
      const codec = codecFromSPS(p.main.length ? p.main : p.aux) ?? 'avc1.640033';
      const key = `${codec}/${w}x${h}`;
      if (!this.d444 || this.d444.key !== key) {
        this.resetDecoders();
        const mk = (view: 'main' | 'aux') => {
          const d = new VideoDecoder({
            output: (f) => this.on444(view, f),
            error: (e) => {
              console.warn('decoder 4:4:4:', e);
              this.decoderFailed('avc444');
            },
          });
          d.configure({ codec, optimizeForLatency: true, hardwareAcceleration: 'prefer-software' });
          return d;
        };
        this.d444 = { main: mk('main'), aux: mk('aux'), key };
      }
      this.needKey = false;
    } else if (this.needKey || !this.d444) {
      this.requestKeyframe();
      return;
    }
    const op: Op = {
      seq,
      at,
      ready: false,
      region: this.region(seq),
      video: true,
      codec: 'avc444',
      wantMain: p.main.length > 0,
      wantAux: p.aux.length > 0,
    };
    if (!op.wantMain && !op.wantAux) return;
    this.push(op);
    try {
      if (op.wantMain) this.d444!.main.decode(new EncodedVideoChunk({ type: p.mainKey ? 'key' : 'delta', timestamp: seq, data: p.main }));
      if (op.wantAux) this.d444!.aux.decode(new EncodedVideoChunk({ type: p.auxKey ? 'key' : 'delta', timestamp: seq, data: p.aux }));
    } catch (e) {
      console.warn(e);
      this.decoderFailed('avc444');
      return;
    }
    // decodificar duas visões em software pesa: se a fila cresce e não volta,
    // este PC não dá conta e o 4:4:4 sai do hello
    const q = Math.max(this.d444!.main.decodeQueueSize, this.d444!.aux.decodeQueueSize);
    if (q > SLOW_QUEUE) {
      this.slowSince ||= performance.now();
      if (performance.now() - this.slowSince > SLOW_FOR_MS) this.decoderFailed('avc444', true);
    } else {
      this.slowSince = 0;
    }
  }

  private on444(view: 'main' | 'aux', f: VideoFrame) {
    const op = this.bySeq.get(f.timestamp);
    if (!op) {
      f.close();
      return;
    }
    readI420(f)
      .then((planes) => {
        if (view === 'main') {
          op.main = planes;
          op.wantMain = false;
        } else {
          op.aux = planes;
          op.wantAux = false;
        }
      })
      .catch((e) => {
        console.warn('4:4:4: planos', e);
        this.decoderFailed('avc444');
      })
      .finally(() => {
        f.close();
        if (op.wantMain || op.wantAux || op.ready) return;
        // a parte que não veio neste quadro (o encoder atrasou) é a última
        const main = op.main ?? this.lastMain;
        const aux = op.aux ?? this.lastAux;
        if (op.main) this.lastMain = op.main;
        if (op.aux) this.lastAux = op.aux;
        if (main) {
          op.draw = () => {
            this.fit(main.w, main.h);
            this.r.yuv444(main, aux, op.region);
          };
        }
        op.ready = true;
        this.flush();
      });
  }
}

/** Lê os planos de um quadro decodificado em software (I420 ou NV12) como I420. */
async function readI420(f: VideoFrame): Promise<I420> {
  const w = f.visibleRect?.width ?? f.codedWidth;
  const h = f.visibleRect?.height ?? f.codedHeight;
  const buf = new Uint8Array(f.allocationSize());
  const layout = await f.copyTo(buf);
  const plane = (i: number, pw: number, ph: number) => {
    const { offset, stride } = layout[i];
    if (stride === pw) return buf.subarray(offset, offset + pw * ph);
    const out = new Uint8Array(pw * ph);
    for (let y = 0; y < ph; y++) out.set(buf.subarray(offset + y * stride, offset + y * stride + pw), y * pw);
    return out;
  };
  const y = plane(0, w, h);
  if (f.format === 'NV12') {
    const uv = plane(1, w, h / 2);
    const u = new Uint8Array((w * h) / 4);
    const v = new Uint8Array((w * h) / 4);
    for (let i = 0, j = 0; i < u.length; i++, j += 2) {
      u[i] = uv[j];
      v[i] = uv[j + 1];
    }
    return { w, h, y, u, v };
  }
  if (f.format !== 'I420') throw new Error(`formato ${f.format} sem suporte no 4:4:4`);
  return { w, h, y, u: plane(1, w / 2, h / 2), v: plane(2, w / 2, h / 2) };
}
