// Desenho da tela remota no canvas. O canvas guarda a tela inteira (é a
// "tela composta"): um quadro de vídeo só pinta as regiões que mudaram
// (mensagem rg), e os retalhos sem perda pintam por cima. Com WebGL2, também
// recompõe a cor 4:4:4 (AVC444) em shader; sem ele, fica o contexto 2D.

/** Retângulo em pixels do quadro (origem no canto de cima, à esquerda). */
export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** Visão decodificada em I420: Y (w x h), U e V (w/2 x h/2), planos contíguos. */
export interface I420 {
  w: number;
  h: number;
  y: Uint8Array<ArrayBuffer>;
  u: Uint8Array<ArrayBuffer>;
  v: Uint8Array<ArrayBuffer>;
}

export interface Renderer {
  /** true = WebGL2 (sabe desenhar o 4:4:4) */
  readonly gl: boolean;
  /** Ajusta o canvas ao tamanho do quadro; true se mudou (a tela voltou preta). */
  size(w: number, h: number): boolean;
  /** Imagem do tamanho do quadro (vídeo decodificado, JPEG), só dentro de rects (null = inteira). */
  image(src: TexImageSource, w: number, h: number, rects: Rect[] | null): void;
  /** Retalho sem perda em (x, y). */
  patch(img: ImageBitmap, x: number, y: number): void;
  /** Cor 4:4:4 das visões principal e auxiliar (null = só a principal), dentro de rects. */
  yuv444(main: I420, aux: I420 | null, rects: Rect[] | null): void;
}

class Canvas2D implements Renderer {
  readonly gl = false;
  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly ctx: CanvasRenderingContext2D,
  ) {}

  size(w: number, h: number) {
    if (this.canvas.width === w && this.canvas.height === h) return false;
    this.canvas.width = w;
    this.canvas.height = h;
    return true;
  }

  image(src: TexImageSource, w: number, h: number, rects: Rect[] | null) {
    const s = src as CanvasImageSource;
    if (!rects) {
      this.ctx.drawImage(s, 0, 0, w, h);
      return;
    }
    for (const r of rects) this.ctx.drawImage(s, r.x, r.y, r.w, r.h, r.x, r.y, r.w, r.h);
  }

  patch(img: ImageBitmap, x: number, y: number) {
    this.ctx.drawImage(img, x, y);
  }

  yuv444() {
    throw new Error('4:4:4 precisa de WebGL2');
  }
}

const VS = `#version 300 es
void main() {
  vec2 p = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));
  gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}`;

// imagem: copia o texel exato (sem filtro) do pixel do quadro
const FS_COPY = `#version 300 es
precision highp float;
precision highp int;
uniform sampler2D tex;
uniform ivec2 off;
uniform int H;
out vec4 o;
void main() {
  ivec2 q = ivec2(int(gl_FragCoord.x), H - 1 - int(gl_FragCoord.y)) - off;
  o = texelFetch(tex, q, 0);
}`;

// AVC444: o mesmo arranjo de internal/avc444 (e de yuv.h no visualizador
// nativo), BT.709 faixa completa
const FS_444 = `#version 300 es
precision highp float;
precision highp int;
uniform highp sampler2D my, mu, mv, ay, au, av;
uniform bool hasAux;
uniform int H;
out vec4 o;
float f(highp sampler2D s, ivec2 p) { return texelFetch(s, p, 0).r * 255.0; }
void main() {
  int x = int(gl_FragCoord.x), y = H - 1 - int(gl_FragCoord.y);
  float Y = f(my, ivec2(x, y)), U, V;
  if (!hasAux) {
    U = f(mu, ivec2(x / 2, y / 2)); V = f(mv, ivec2(x / 2, y / 2));
  } else if ((y & 1) == 1) {
    U = f(ay, ivec2(x, y / 2)); V = f(ay, ivec2(x, H / 2 + y / 2));
  } else if ((x & 1) == 0) {
    U = f(mu, ivec2(x / 2, y / 2)); V = f(mv, ivec2(x / 2, y / 2));
  } else {
    U = f(au, ivec2(x / 2, y / 2)); V = f(av, ivec2(x / 2, y / 2));
  }
  U -= 128.0; V -= 128.0;
  vec3 rgb = vec3(Y + 1.5748 * V, Y - 0.1873 * U - 0.4681 * V, Y + 1.8556 * U);
  o = vec4(clamp(rgb / 255.0, 0.0, 1.0), 1.0);
}`;

class GL implements Renderer {
  readonly gl = true;
  private readonly copy: WebGLProgram;
  private readonly p444: WebGLProgram;
  private readonly tex: WebGLTexture;
  private readonly planes: WebGLTexture[];
  private readonly vao: WebGLVertexArrayObject;

  constructor(
    private readonly canvas: HTMLCanvasElement,
    private readonly g: WebGL2RenderingContext,
  ) {
    this.copy = program(g, FS_COPY);
    this.p444 = program(g, FS_444);
    this.vao = g.createVertexArray()!;
    this.tex = texture(g);
    this.planes = Array.from({ length: 6 }, () => texture(g));
    g.useProgram(this.p444);
    ['my', 'mu', 'mv', 'ay', 'au', 'av'].forEach((n, i) => g.uniform1i(g.getUniformLocation(this.p444, n), i + 1));
    g.useProgram(this.copy);
    g.uniform1i(g.getUniformLocation(this.copy, 'tex'), 0);
    g.pixelStorei(g.UNPACK_ALIGNMENT, 1);
  }

  size(w: number, h: number) {
    if (this.canvas.width === w && this.canvas.height === h) return false;
    this.canvas.width = w;
    this.canvas.height = h;
    this.g.viewport(0, 0, w, h);
    return true;
  }

  /** Pinta o programa atual só dentro dos retângulos (tesoura). */
  private paint(rects: Rect[]) {
    const g = this.g;
    const H = this.canvas.height;
    g.bindVertexArray(this.vao);
    g.enable(g.SCISSOR_TEST);
    for (const r of rects) {
      if (r.w <= 0 || r.h <= 0) continue;
      g.scissor(r.x, H - r.y - r.h, r.w, r.h);
      g.drawArrays(g.TRIANGLES, 0, 3);
    }
    g.disable(g.SCISSOR_TEST);
  }

  private copyFrom(off: [number, number]) {
    const g = this.g;
    g.useProgram(this.copy);
    g.uniform2i(g.getUniformLocation(this.copy, 'off'), off[0], off[1]);
    g.uniform1i(g.getUniformLocation(this.copy, 'H'), this.canvas.height);
    g.activeTexture(g.TEXTURE0);
    g.bindTexture(g.TEXTURE_2D, this.tex);
  }

  image(src: TexImageSource, w: number, h: number, rects: Rect[] | null) {
    const g = this.g;
    this.copyFrom([0, 0]);
    g.texImage2D(g.TEXTURE_2D, 0, g.RGBA8, g.RGBA, g.UNSIGNED_BYTE, src);
    this.paint(rects ?? [{ x: 0, y: 0, w, h }]);
  }

  patch(img: ImageBitmap, x: number, y: number) {
    const g = this.g;
    this.copyFrom([x, y]);
    g.texImage2D(g.TEXTURE_2D, 0, g.RGBA8, g.RGBA, g.UNSIGNED_BYTE, img);
    this.paint([{ x, y, w: img.width, h: img.height }]);
  }

  yuv444(main: I420, aux: I420 | null, rects: Rect[] | null) {
    const g = this.g;
    const up = (i: number, data: Uint8Array<ArrayBuffer>, w: number, h: number) => {
      g.activeTexture(g.TEXTURE1 + i);
      g.bindTexture(g.TEXTURE_2D, this.planes[i]);
      g.texImage2D(g.TEXTURE_2D, 0, g.R8, w, h, 0, g.RED, g.UNSIGNED_BYTE, data);
    };
    const { w, h } = main;
    up(0, main.y, w, h);
    up(1, main.u, w / 2, h / 2);
    up(2, main.v, w / 2, h / 2);
    const withAux = !!aux && aux.w === w && aux.h === h;
    if (withAux) {
      up(3, aux.y, w, h);
      up(4, aux.u, w / 2, h / 2);
      up(5, aux.v, w / 2, h / 2);
    }
    g.useProgram(this.p444);
    g.uniform1i(g.getUniformLocation(this.p444, 'hasAux'), withAux ? 1 : 0);
    g.uniform1i(g.getUniformLocation(this.p444, 'H'), this.canvas.height);
    this.paint(rects ?? [{ x: 0, y: 0, w, h }]);
  }
}

function texture(g: WebGL2RenderingContext) {
  const t = g.createTexture()!;
  g.bindTexture(g.TEXTURE_2D, t);
  g.texParameteri(g.TEXTURE_2D, g.TEXTURE_MIN_FILTER, g.NEAREST);
  g.texParameteri(g.TEXTURE_2D, g.TEXTURE_MAG_FILTER, g.NEAREST);
  g.texParameteri(g.TEXTURE_2D, g.TEXTURE_WRAP_S, g.CLAMP_TO_EDGE);
  g.texParameteri(g.TEXTURE_2D, g.TEXTURE_WRAP_T, g.CLAMP_TO_EDGE);
  return t;
}

function program(g: WebGL2RenderingContext, fs: string) {
  const sh = (type: number, src: string) => {
    const s = g.createShader(type)!;
    g.shaderSource(s, src);
    g.compileShader(s);
    if (!g.getShaderParameter(s, g.COMPILE_STATUS)) throw new Error(g.getShaderInfoLog(s) ?? 'shader');
    return s;
  };
  const p = g.createProgram()!;
  g.attachShader(p, sh(g.VERTEX_SHADER, VS));
  g.attachShader(p, sh(g.FRAGMENT_SHADER, fs));
  g.linkProgram(p);
  if (!g.getProgramParameter(p, g.LINK_STATUS)) throw new Error(g.getProgramInfoLog(p) ?? 'program');
  return p;
}

/**
 * Cria o desenho do canvas: WebGL2 se o navegador tiver (e os shaders
 * compilarem), senão o contexto 2D. preserveDrawingBuffer mantém a tela
 * entre os quadros, como o 2D: é o que deixa o vídeo pintar só as regiões
 * que mudaram.
 */
export function createRenderer(canvas: HTMLCanvasElement, allowGL = true): Renderer {
  const opts: WebGLContextAttributes = {
    alpha: false,
    antialias: false,
    depth: false,
    stencil: false,
    preserveDrawingBuffer: true,
    desynchronized: true,
  };
  // os shaders são testados antes num canvas descartável: um canvas que já
  // abriu WebGL não aceita mais o contexto 2D
  let ok = false;
  if (allowGL) {
    const probe = document.createElement('canvas');
    const g = probe.getContext('webgl2', opts);
    if (g) {
      try {
        new GL(probe, g);
        ok = true;
      } catch (e) {
        console.warn('WebGL2 sem os shaders do console; usando o 2D', e);
      }
      g.getExtension('WEBGL_lose_context')?.loseContext();
    }
  }
  if (ok) {
    const g = canvas.getContext('webgl2', opts);
    if (g) return new GL(canvas, g);
  }
  const ctx = canvas.getContext('2d', { alpha: false, desynchronized: true });
  if (!ctx) throw new Error('canvas sem contexto 2D');
  return new Canvas2D(canvas, ctx);
}
