// Espelho de internal/protocol (Go). Qualquer mudança lá DEVE ser feita aqui.

export const HEADER_SIZE = 12;
export const CODEC_H264 = 1;
export const CODEC_JPEG = 2;

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
  | { t: 'hello'; h264: boolean }
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
