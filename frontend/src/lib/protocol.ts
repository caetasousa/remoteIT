// Espelho de internal/protocol (Go). Qualquer mudança lá DEVE ser feita aqui.

export const HEADER_SIZE = 12;
export const CODEC_H264 = 1;
export const CODEC_JPEG = 2;
/** Imagem PNG sem perda enviada quando a tela fica parada (refresh de nitidez). */
export const CODEC_PNG = 3;
/**
 * Cor 4:4:4 em dois fluxos H.264 (o arranjo está em internal/avc444): o
 * console decodifica as duas visões em software e recompõe em WebGL.
 */
export const CODEC_AVC444 = 4;
/** AV1 em OBUs com tamanho ("low overhead"), uma unidade temporal por quadro. */
export const CODEC_AV1 = 5;
/** Retalhos PNG sem perda das partes da tela que pararam de mudar (veja parsePNGRects). */
export const CODEC_PNG_RECT = 6;

/** Nomes dos codecs no hello, em ordem de preferência. */
export type CodecName = 'avc444' | 'av1' | 'h264' | 'jpeg';

/** Cabeçalho de 12 bytes (big-endian) de todo quadro binário de vídeo. */
export interface FrameHeader {
  codec: number;
  keyframe: boolean;
  w: number;
  h: number;
  seq: number;
}

export function parseFrame(buf: ArrayBuffer): { header: FrameHeader; data: Uint8Array<ArrayBuffer> } {
  const dv = new DataView(buf);
  return {
    header: {
      codec: dv.getUint8(0),
      keyframe: (dv.getUint8(1) & 1) === 1,
      w: dv.getUint16(2),
      h: dv.getUint16(4),
      seq: dv.getUint32(6),
    },
    data: new Uint8Array(buf, HEADER_SIZE),
  };
}

/** Um retalho sem perda: PNG para desenhar em (x, y). */
export interface PNGRect {
  x: number;
  y: number;
  png: Uint8Array<ArrayBuffer>;
}

/** Separa o payload de CODEC_PNG_RECT: x (2) | y (2) | tamanho (4) | PNG, repetido. */
export function parsePNGRects(data: Uint8Array<ArrayBuffer>): PNGRect[] {
  const out: PNGRect[] = [];
  const dv = new DataView(data.buffer, data.byteOffset, data.byteLength);
  let o = 0;
  while (o + 8 <= data.length) {
    const n = dv.getUint32(o + 4);
    if (o + 8 + n > data.length) break;
    out.push({ x: dv.getUint16(o), y: dv.getUint16(o + 2), png: data.subarray(o + 8, o + 8 + n) });
    o += 8 + n;
  }
  return out;
}

/** Payload AVC444: flags (1) | tamanho da principal (4) | principal | auxiliar. */
export function parseAVC444(
  data: Uint8Array<ArrayBuffer>,
): { main: Uint8Array<ArrayBuffer>; aux: Uint8Array<ArrayBuffer>; mainKey: boolean; auxKey: boolean } | null {
  if (data.length < 5) return null;
  const n = new DataView(data.buffer, data.byteOffset + 1, 4).getUint32(0);
  if (n > data.length - 5) return null;
  return {
    main: data.subarray(5, 5 + n),
    aux: data.subarray(5 + n),
    mainKey: (data[0] & 1) !== 0,
    auxKey: (data[0] & 2) !== 0,
  };
}

/** Mensagens do técnico para o agente. */
export type ClientMessage =
  // codecs em ordem de preferência; h264 é para agentes de antes da negociação
  | { t: 'hello'; h264: boolean; codecs: CodecName[] }
  // p é o preset; vw x vh a área onde a tela aparece, em pixels físicos (0 =
  // nativa). Vão sempre juntos: um cfg sem a área é lido como "nativa"
  | { t: 'cfg'; p: Preset; vw: number; vh: number }
  | { t: 'kf' }
  | { t: 'ack'; s: number }
  | { t: 'mm'; x: number; y: number }
  | { t: 'md' | 'mu'; x: number; y: number; b: number }
  | { t: 'wh'; d: number }
  | { t: 'kd' | 'ku'; code: string; key: string }
  // pede para bloquear a estação de trabalho (equivalente a Win+L); o agente
  // chama LockWorkStation em vez de simular a tecla, que o Windows recusa
  // vinda de entrada sintética (veja internal/agent/lock)
  | { t: 'lock' };

/** Mensagens do agente para o técnico. */
export type ServerMessage =
  | { t: 'waiting' | 'granted' | 'denied' | 'secure' }
  | {
      t: 'stats';
      enc: string;
      fps: number;
      kbps: number;
      target: number;
      rtt: number;
      w: number;
      h: number;
      /** menor RTT recente (a rede sem fila); ausente em agentes antigos */
      rttmin?: number;
      /** captura + codificação de um quadro no PC remoto, em µs */
      encus?: number;
    }
  // onde vale o próximo quadro de vídeo s: x, y, largura, altura de cada
  // retângulo (vazio = em lugar nenhum); sem rg, o quadro vale inteiro
  | { t: 'rg'; s: number; r: number[] }
  | { t: 'cs'; c: string }
  | { t: 'cur'; x: number; y: number; v: boolean };

export type Preset = 'turbo' | 'balanced' | 'quality' | 'quality60';

export const PRESETS: { value: Preset; label: string }[] = [
  { value: 'turbo', label: 'Ultra-rápido (1280p 60fps)' },
  { value: 'balanced', label: 'Equilibrado (1080p 60fps)' },
  { value: 'quality', label: 'Nítido (nativa 60fps)' },
  { value: 'quality60', label: 'Máximo (nativa 60fps alta banda)' },
];
