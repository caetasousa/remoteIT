// Espelho de internal/protocol (Go). Qualquer mudança lá DEVE ser feita aqui.

export const HEADER_SIZE = 12;
export const CODEC_H264 = 1;
export const CODEC_JPEG = 2;
/** Imagem PNG sem perda enviada quando a tela fica parada (refresh de nitidez). */
export const CODEC_PNG = 3;
/** Cor 4:4:4 em dois fluxos H.264: só o visualizador nativo decodifica. */
export const CODEC_AVC444 = 4;
/** AV1 em OBUs com tamanho ("low overhead"), uma unidade temporal por quadro. */
export const CODEC_AV1 = 5;

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

/** Mensagens do técnico para o agente. */
export type ClientMessage =
  // codecs em ordem de preferência; h264 é para agentes de antes da negociação
  | { t: 'hello'; h264: boolean; codecs: CodecName[] }
  | { t: 'cfg'; p: Preset }
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
  | { t: 'stats'; enc: string; fps: number; kbps: number; target: number; rtt: number; w: number; h: number }
  | { t: 'cs'; c: string }
  | { t: 'cur'; x: number; y: number; v: boolean };

export type Preset = 'turbo' | 'balanced' | 'quality' | 'quality60';

export const PRESETS: { value: Preset; label: string }[] = [
  { value: 'turbo', label: 'Ultra-rápido (1280p 60fps)' },
  { value: 'balanced', label: 'Equilibrado (1080p 60fps)' },
  { value: 'quality', label: 'Nítido (nativa 60fps)' },
  { value: 'quality60', label: 'Máximo (nativa 60fps alta banda)' },
];
