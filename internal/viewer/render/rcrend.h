// rcrend.h - desenho do visualizador nativo na GPU (Direct3D 11).
//
// O caminho de cada quadro fica todo na placa de vídeo: o decodificador
// (internal/viewer/decode, rd_decode_tex) entrega o NV12 numa textura deste
// dispositivo; um shader converte para RGB (H.264/AV1: BT.709 16-235; AVC444:
// recompõe a cor das duas visões, BT.709 0-255, como yuv.h) e desenha na
// textura "composta", que guarda a tela inteira. O vídeo só entra nas
// regiões que mudaram (protocol.Region): o resto continua com os retalhos
// sem perda, que sobem direto para a composta. Para a janela, um último
// passe ajusta o tamanho: média da área para reduzir (o filtro de
// internal/scale), bilinear para ampliar.
//
// Antes, o visualizador copiava cada quadro da GPU para a memória,
// convertia na CPU e desenhava com GDI: ~5 ms (1080p) a ~18 ms (4K) só de
// conversão, fora a cópia e o StretchDIBits.
#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <d3d10.h>
#include <d3dcompiler.h>
#include <dxgi1_2.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef HRESULT (WINAPI *pfnRRCompile)(const void *, SIZE_T, const char *, const D3D_SHADER_MACRO *, ID3DInclude *,
	const char *, const char *, UINT, UINT, ID3DBlob **, ID3DBlob **);
static pfnRRCompile rrD3DCompile;
static int rr_compiler(void) {
	static int state = 0;
	if (state) return state;
	HMODULE m = LoadLibraryW(L"d3dcompiler_47.dll");
	state = -1;
	if (m && (rrD3DCompile = (pfnRRCompile)(void *)GetProcAddress(m, "D3DCompile"))) state = 1;
	return state;
}

#define RR_NV12      0 // H.264 / AV1: uma visão NV12, BT.709 16-235
#define RR_AVC444    1 // duas visões (principal e auxiliar), BT.709 0-255
#define RR_AVC444_M  2 // só a principal do AVC444 (a auxiliar ainda não chegou)

#define RR_REL(p) do { if (p) { (p)->lpVtbl->Release(p); (p) = NULL; } } while (0)

typedef struct rr_ctx {
	ID3D11Device *dev;
	ID3D11DeviceContext *ctx;
	IDXGISwapChain *sc; // NULL no autoteste (desenha numa textura)
	ID3D11RenderTargetView *bb;
	int bw, bh;
	ID3D11VertexShader *vs;
	ID3D11PixelShader *ps_nv12, *ps_444, *ps_444m, *ps_present;
	ID3D11RasterizerState *rs;
	ID3D11SamplerState *lin;
	ID3D11Buffer *cb;
	// visões do vídeo (0 = principal, 1 = auxiliar no AVC444): a NV12 em que
	// o decodificador entrega (rr_video), ou os planos Y e UV separados
	// quando o quadro sobe da memória (rr_upload_nv12)
	ID3D11Texture2D *nv12[2], *py[2], *puv[2];
	ID3D11ShaderResourceView *sy[2], *suv[2];
	int vw[2], vh[2];
	// a tela composta
	ID3D11Texture2D *comp;
	ID3D11RenderTargetView *comp_rtv;
	ID3D11ShaderResourceView *comp_srv;
	int cw, ch;
	int flip; // modelo de troca "flip" (Windows 8+)
} rr_ctx;

static void rr_drop_view(rr_ctx *r, int v);
ID3D11Texture2D *rr_video(rr_ctx *r, int v, int w, int h);

// Constantes dos shaders: info = (largura, altura) do quadro e, no passe
// final, dst = retângulo de destino na janela e scale = (fx, fy, taps x,
// taps y) da redução.
typedef struct {
	float dst[4];
	float src[4];
	int info[4];
} rr_consts;

static const char rr_hlsl[] =
	"cbuffer K : register(b0) { float4 dst; float4 src; int4 info; };\n"
	"Texture2D<float> ty : register(t0);\n"
	"Texture2D<float2> tuv : register(t1);\n"
	"Texture2D<float> ay : register(t2);\n"
	"Texture2D<float2> auv : register(t3);\n"
	"Texture2D<float4> comp : register(t4);\n"
	"SamplerState lin : register(s0);\n"
	"void vs(uint id : SV_VertexID, out float4 pos : SV_Position) {\n"
	"  float2 t = float2((id << 1) & 2, id & 2);\n"
	"  pos = float4(t * float2(2, -2) + float2(-1, 1), 0, 1);\n"
	"}\n"
	// H.264/AV1: BT.709 faixa limitada, cor interpolada na posição padrão do
	// H.264 (mesma conta inteira de yuv.h nv12_to_bgra)
	"int2 cuv(int2 c) { return int2(round(tuv.Load(int3(c, 0)) * 255.0)); }\n"
	"int2 chroma_h(int x, int j, int cw) {\n"
	"  int i = x >> 1;\n"
	"  if ((x & 1) == 0) return 2 * cuv(int2(i, j));\n"
	"  return cuv(int2(i, j)) + cuv(int2(min(i + 1, cw - 1), j));\n"
	"}\n"
	"float4 ps_nv12(float4 p : SV_Position) : SV_Target {\n"
	"  int2 q = int2(p.xy);\n"
	"  int cw = (info.x + 1) / 2, ch = (info.y + 1) / 2;\n"
	"  int j = q.y >> 1, jn = (q.y & 1) ? min(j + 1, ch - 1) : max(j - 1, 0);\n"
	"  int2 s = (3 * chroma_h(q.x, j, cw) + chroma_h(q.x, jn, cw) + 4) >> 3;\n"
	"  float Y = 1.164384 * (ty.Load(int3(q, 0)) * 255.0 - 16.0);\n"
	"  float2 c = float2(s) - 128.0;\n"
	"  float3 rgb = float3(Y + 1.792741 * c.y, Y - 0.213249 * c.x - 0.532909 * c.y, Y + 2.112402 * c.x);\n"
	"  return float4(saturate(rgb / 255.0), 1);\n"
	"}\n"
	// AVC444: BT.709 faixa completa (internal/avc444.ToRGB)
	"float4 full(float Y, float U, float V) {\n"
	"  U -= 128.0; V -= 128.0;\n"
	"  float3 rgb = float3(Y + 1.5748 * V, Y - 0.1873 * U - 0.4681 * V, Y + 1.8556 * U);\n"
	"  return float4(saturate(rgb / 255.0), 1);\n"
	"}\n"
	"float4 ps_444(float4 p : SV_Position) : SV_Target {\n"
	"  int x = (int)p.x, y = (int)p.y;\n"
	"  float Y = ty.Load(int3(x, y, 0)) * 255.0;\n"
	"  if (y & 1) {\n" // linha ímpar: U e V inteiros no Y auxiliar (U em cima, V embaixo)
	"    return full(Y, ay.Load(int3(x, y / 2, 0)) * 255.0, ay.Load(int3(x, info.y / 2 + y / 2, 0)) * 255.0);\n"
	"  }\n"
	"  float2 c = (x & 1) ? auv.Load(int3(x / 2, y / 2, 0)) : tuv.Load(int3(x / 2, y / 2, 0));\n"
	"  return full(Y, c.x * 255.0, c.y * 255.0);\n"
	"}\n"
	"float4 ps_444m(float4 p : SV_Position) : SV_Target {\n"
	"  int2 q = int2(p.xy);\n"
	"  float2 c = tuv.Load(int3(q / 2, 0)) * 255.0;\n"
	"  return full(ty.Load(int3(q, 0)) * 255.0, c.x, c.y);\n"
	"}\n"
	// passe final: a composta no retângulo dst da janela. Redução: média da
	// área (internal/scale.Area); ampliação ou 1:1: bilinear (no 1:1 cai
	// exatamente no centro de cada pixel, então é cópia).
	"float4 ps_present(float4 p : SV_Position) : SV_Target {\n"
	"  float2 o = p.xy - dst.xy;\n"
	"  if (src.x <= 1.0 && src.y <= 1.0) return comp.SampleLevel(lin, o / dst.zw, 0);\n"
	"  float x0 = floor(o.x) * src.x, y0 = floor(o.y) * src.y, x1 = x0 + src.x, y1 = y0 + src.y;\n"
	"  int i0 = (int)floor(x0), j0 = (int)floor(y0);\n"
	"  float4 acc = float4(0, 0, 0, 0);\n"
	"  for (int j = 0; j < info.w; j++) {\n"
	"    float yy = (float)(j0 + j);\n"
	"    float wy = max(0.0, min(yy + 1.0, y1) - max(yy, y0));\n"
	"    for (int i = 0; i < info.z; i++) {\n"
	"      float xx = (float)(i0 + i);\n"
	"      float wx = max(0.0, min(xx + 1.0, x1) - max(xx, x0));\n"
	"      acc += comp.Load(int3(min(i0 + i, info.x - 1), min(j0 + j, info.y - 1), 0)) * (wx * wy);\n"
	"    }\n"
	"  }\n"
	"  return acc / (src.x * src.y);\n"
	"}\n";

static HRESULT rr_ps(rr_ctx *r, const char *entry, ID3D11PixelShader **ps, char *err, int errlen) {
	ID3DBlob *code = NULL, *msg = NULL;
	HRESULT hr = rrD3DCompile(rr_hlsl, sizeof rr_hlsl - 1, "rcrend.hlsl", NULL, NULL, entry, "ps_4_0",
	                          D3DCOMPILE_OPTIMIZATION_LEVEL3, 0, &code, &msg);
	if (FAILED(hr)) {
		snprintf(err, errlen, "shader %s: 0x%08lx %s", entry, (unsigned long)hr, msg ? (const char *)ID3D10Blob_GetBufferPointer(msg) : "");
	} else {
		hr = ID3D11Device_CreatePixelShader(r->dev, ID3D10Blob_GetBufferPointer(code), ID3D10Blob_GetBufferSize(code), NULL, ps);
		if (FAILED(hr)) snprintf(err, errlen, "CreatePixelShader %s: 0x%08lx", entry, (unsigned long)hr);
	}
	RR_REL(code);
	RR_REL(msg);
	return hr;
}

// rr_setup compila os shaders e cria os estados fixos.
static int rr_setup(rr_ctx *r, char *err, int errlen) {
	if (rr_compiler() != 1) { snprintf(err, errlen, "d3dcompiler_47.dll indisponível"); return -1; }
	ID3DBlob *code = NULL, *msg = NULL;
	HRESULT hr = rrD3DCompile(rr_hlsl, sizeof rr_hlsl - 1, "rcrend.hlsl", NULL, NULL, "vs", "vs_4_0",
	                          D3DCOMPILE_OPTIMIZATION_LEVEL3, 0, &code, &msg);
	if (FAILED(hr)) {
		snprintf(err, errlen, "shader vs: 0x%08lx %s", (unsigned long)hr, msg ? (const char *)ID3D10Blob_GetBufferPointer(msg) : "");
		RR_REL(msg);
		return -1;
	}
	hr = ID3D11Device_CreateVertexShader(r->dev, ID3D10Blob_GetBufferPointer(code), ID3D10Blob_GetBufferSize(code), NULL, &r->vs);
	RR_REL(code);
	RR_REL(msg);
	if (FAILED(hr)) { snprintf(err, errlen, "CreateVertexShader: 0x%08lx", (unsigned long)hr); return -1; }
	if (FAILED(rr_ps(r, "ps_nv12", &r->ps_nv12, err, errlen)) || FAILED(rr_ps(r, "ps_444", &r->ps_444, err, errlen)) ||
	    FAILED(rr_ps(r, "ps_444m", &r->ps_444m, err, errlen)) || FAILED(rr_ps(r, "ps_present", &r->ps_present, err, errlen)))
		return -1;
	D3D11_RASTERIZER_DESC rd;
	memset(&rd, 0, sizeof rd);
	rd.FillMode = D3D11_FILL_SOLID;
	rd.CullMode = D3D11_CULL_NONE;
	rd.DepthClipEnable = TRUE;
	rd.ScissorEnable = TRUE; // as regiões de vídeo são retângulos de tesoura
	if (FAILED(ID3D11Device_CreateRasterizerState(r->dev, &rd, &r->rs))) { snprintf(err, errlen, "RasterizerState"); return -1; }
	D3D11_SAMPLER_DESC sd;
	memset(&sd, 0, sizeof sd);
	sd.Filter = D3D11_FILTER_MIN_MAG_MIP_LINEAR;
	sd.AddressU = sd.AddressV = sd.AddressW = D3D11_TEXTURE_ADDRESS_CLAMP;
	sd.MaxLOD = D3D11_FLOAT32_MAX;
	if (FAILED(ID3D11Device_CreateSamplerState(r->dev, &sd, &r->lin))) { snprintf(err, errlen, "SamplerState"); return -1; }
	D3D11_BUFFER_DESC bd;
	memset(&bd, 0, sizeof bd);
	bd.ByteWidth = sizeof(rr_consts);
	bd.Usage = D3D11_USAGE_DEFAULT;
	bd.BindFlags = D3D11_BIND_CONSTANT_BUFFER;
	if (FAILED(ID3D11Device_CreateBuffer(r->dev, &bd, NULL, &r->cb))) { snprintf(err, errlen, "ConstantBuffer"); return -1; }
	return 0;
}

static int rr_device(rr_ctx *r, int warp_ok, char *err, int errlen) {
	D3D_FEATURE_LEVEL lv[] = {D3D_FEATURE_LEVEL_11_1, D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1, D3D_FEATURE_LEVEL_10_0};
	UINT fl = D3D11_CREATE_DEVICE_BGRA_SUPPORT | D3D11_CREATE_DEVICE_VIDEO_SUPPORT;
	HRESULT hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, fl, lv, 4, D3D11_SDK_VERSION, &r->dev, NULL, &r->ctx);
	if (hr == E_INVALIDARG)
		hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, fl, lv + 1, 3, D3D11_SDK_VERSION, &r->dev, NULL, &r->ctx);
	if (FAILED(hr)) // sem suporte a vídeo no dispositivo: o decodificador sobe pela CPU
		hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, D3D11_CREATE_DEVICE_BGRA_SUPPORT, lv + 1, 3, D3D11_SDK_VERSION, &r->dev, NULL, &r->ctx);
	if (FAILED(hr) && warp_ok)
		hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_WARP, NULL, D3D11_CREATE_DEVICE_BGRA_SUPPORT, lv + 1, 3, D3D11_SDK_VERSION, &r->dev, NULL, &r->ctx);
	if (FAILED(hr)) { snprintf(err, errlen, "D3D11CreateDevice: 0x%08lx", (unsigned long)hr); return -1; }
	ID3D10Multithread *mt = NULL;
	if (SUCCEEDED(ID3D11Device_QueryInterface(r->dev, &IID_ID3D10Multithread, (void **)&mt))) {
		ID3D10Multithread_SetMultithreadProtected(mt, TRUE); // decodificador, desenho e janela em threads diferentes
		RR_REL(mt);
	}
	return 0;
}

void rr_close(rr_ctx *r) {
	if (!r) return;
	for (int v = 0; v < 2; v++) { RR_REL(r->sy[v]); RR_REL(r->suv[v]); RR_REL(r->nv12[v]); RR_REL(r->py[v]); RR_REL(r->puv[v]); }
	RR_REL(r->comp_srv); RR_REL(r->comp_rtv); RR_REL(r->comp);
	RR_REL(r->cb); RR_REL(r->lin); RR_REL(r->rs);
	RR_REL(r->ps_present); RR_REL(r->ps_444m); RR_REL(r->ps_444); RR_REL(r->ps_nv12); RR_REL(r->vs);
	RR_REL(r->bb); RR_REL(r->sc);
	if (r->ctx) ID3D11DeviceContext_ClearState(r->ctx);
	RR_REL(r->ctx); RR_REL(r->dev);
	free(r);
}

// rr_backbuffer (re)cria a vista do buffer da janela.
static int rr_backbuffer(rr_ctx *r) {
	ID3D11Texture2D *bb = NULL;
	RR_REL(r->bb);
	if (FAILED(IDXGISwapChain_GetBuffer(r->sc, 0, &IID_ID3D11Texture2D, (void **)&bb))) return -1;
	HRESULT hr = ID3D11Device_CreateRenderTargetView(r->dev, (ID3D11Resource *)bb, NULL, &r->bb);
	RR_REL(bb);
	return FAILED(hr) ? -1 : 0;
}

// rr_open cria o dispositivo e a cadeia de troca da janela hwnd (w x h).
int rr_open(rr_ctx **out, uintptr_t hwnd_, int w, int h, char *err, int errlen) {
	HWND hwnd = (HWND)hwnd_;
	rr_ctx *r = calloc(1, sizeof *r);
	IDXGIDevice *xd = NULL;
	IDXGIAdapter *ad = NULL;
	IDXGIFactory *f1 = NULL;
	IDXGIFactory2 *f2 = NULL;
	*out = NULL;
	if (w < 1) w = 1;
	if (h < 1) h = 1;
	if (rr_device(r, 0, err, errlen) != 0 || rr_setup(r, err, errlen) != 0) goto fail;
	if (FAILED(ID3D11Device_QueryInterface(r->dev, &IID_IDXGIDevice, (void **)&xd)) ||
	    FAILED(IDXGIDevice_GetAdapter(xd, &ad)) || FAILED(IDXGIAdapter_GetParent(ad, &IID_IDXGIFactory, (void **)&f1))) {
		snprintf(err, errlen, "fábrica DXGI indisponível");
		goto fail;
	}
	// modelo "flip" (Windows 8+): menor latência e sem cópia extra; o
	// Present(0) não espera o vsync, e com o DWM compondo a janela não há
	// rasgo: sai sempre o quadro mais novo
	if (SUCCEEDED(IDXGIFactory_QueryInterface(f1, &IID_IDXGIFactory2, (void **)&f2))) {
		DXGI_SWAP_CHAIN_DESC1 d;
		memset(&d, 0, sizeof d);
		d.Width = w; d.Height = h; d.Format = DXGI_FORMAT_B8G8R8A8_UNORM; d.SampleDesc.Count = 1;
		d.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT; d.BufferCount = 2; d.AlphaMode = DXGI_ALPHA_MODE_IGNORE;
		IDXGISwapChain1 *sc1 = NULL;
		static const DXGI_SWAP_EFFECT effects[] = {4 /* FLIP_DISCARD, Windows 10 */, DXGI_SWAP_EFFECT_FLIP_SEQUENTIAL};
		for (int i = 0; i < 2 && !sc1; i++) {
			d.SwapEffect = effects[i];
			if (FAILED(IDXGIFactory2_CreateSwapChainForHwnd(f2, (IUnknown *)r->dev, hwnd, &d, NULL, NULL, &sc1))) sc1 = NULL;
		}
		if (sc1) {
			IDXGISwapChain1_QueryInterface(sc1, &IID_IDXGISwapChain, (void **)&r->sc);
			RR_REL(sc1);
			r->flip = 1;
		}
	}
	if (!r->sc) { // Windows 7: modelo antigo, por cópia
		DXGI_SWAP_CHAIN_DESC d;
		memset(&d, 0, sizeof d);
		d.BufferDesc.Width = w; d.BufferDesc.Height = h; d.BufferDesc.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
		d.SampleDesc.Count = 1; d.BufferUsage = DXGI_USAGE_RENDER_TARGET_OUTPUT; d.BufferCount = 1;
		d.OutputWindow = hwnd; d.Windowed = TRUE; d.SwapEffect = DXGI_SWAP_EFFECT_DISCARD;
		if (FAILED(IDXGIFactory_CreateSwapChain(f1, (IUnknown *)r->dev, &d, &r->sc))) {
			snprintf(err, errlen, "CreateSwapChain");
			goto fail;
		}
	}
	IDXGIFactory_MakeWindowAssociation(f1, hwnd, DXGI_MWA_NO_ALT_ENTER | DXGI_MWA_NO_WINDOW_CHANGES); // tela cheia é nossa
	// o decodificador entrega NV12 e os shaders leem os planos dela: sem
	// isso (placa ou driver antigos), o visualizador desenha pela CPU
	if (!rr_video(r, 0, 64, 64)) { snprintf(err, errlen, "a placa não lê NV12 em shader"); goto fail; }
	rr_drop_view(r, 0);
	r->bw = w; r->bh = h;
	if (rr_backbuffer(r) != 0) { snprintf(err, errlen, "buffer da janela"); goto fail; }
	RR_REL(f2); RR_REL(f1); RR_REL(ad); RR_REL(xd);
	*out = r;
	return 0;
fail:
	RR_REL(f2); RR_REL(f1); RR_REL(ad); RR_REL(xd);
	rr_close(r);
	return -1;
}

ID3D11Device *rr_dev(rr_ctx *r) { return r->dev; }

// rr_resize acompanha o tamanho da janela.
int rr_resize(rr_ctx *r, int w, int h) {
	if (!r->sc || w < 1 || h < 1 || (w == r->bw && h == r->bh)) return 0;
	RR_REL(r->bb);
	ID3D11DeviceContext_OMSetRenderTargets(r->ctx, 0, NULL, NULL);
	if (FAILED(IDXGISwapChain_ResizeBuffers(r->sc, 0, w, h, DXGI_FORMAT_UNKNOWN, 0))) return -1;
	r->bw = w; r->bh = h;
	return rr_backbuffer(r);
}

static void rr_drop_view(rr_ctx *r, int v) {
	RR_REL(r->sy[v]); RR_REL(r->suv[v]); RR_REL(r->nv12[v]); RR_REL(r->py[v]); RR_REL(r->puv[v]);
	r->vw[v] = r->vh[v] = 0;
}

// rr_video devolve a textura NV12 da visão v no tamanho w x h (recria se
// mudou): é onde o decodificador entrega o quadro, sem passar pela memória.
// Os shaders leem os dois planos por vistas R8 e R8G8 da própria NV12.
ID3D11Texture2D *rr_video(rr_ctx *r, int v, int w, int h) {
	if (v < 0 || v > 1 || w < 2 || h < 2) return NULL;
	if (r->nv12[v] && r->vw[v] == w && r->vh[v] == h) return r->nv12[v];
	rr_drop_view(r, v);
	D3D11_TEXTURE2D_DESC td;
	memset(&td, 0, sizeof td);
	td.Width = w; td.Height = h; td.MipLevels = 1; td.ArraySize = 1; td.Format = DXGI_FORMAT_NV12;
	td.SampleDesc.Count = 1; td.Usage = D3D11_USAGE_DEFAULT; td.BindFlags = D3D11_BIND_SHADER_RESOURCE;
	if (FAILED(ID3D11Device_CreateTexture2D(r->dev, &td, NULL, &r->nv12[v]))) return NULL;
	D3D11_SHADER_RESOURCE_VIEW_DESC sd;
	memset(&sd, 0, sizeof sd);
	sd.ViewDimension = D3D11_SRV_DIMENSION_TEXTURE2D;
	sd.Texture2D.MipLevels = 1;
	sd.Format = DXGI_FORMAT_R8_UNORM;
	if (FAILED(ID3D11Device_CreateShaderResourceView(r->dev, (ID3D11Resource *)r->nv12[v], &sd, &r->sy[v]))) goto fail;
	sd.Format = DXGI_FORMAT_R8G8_UNORM;
	if (FAILED(ID3D11Device_CreateShaderResourceView(r->dev, (ID3D11Resource *)r->nv12[v], &sd, &r->suv[v]))) goto fail;
	r->vw[v] = w; r->vh[v] = h;
	return r->nv12[v];
fail:
	rr_drop_view(r, v);
	return NULL;
}

static HRESULT rr_plane(rr_ctx *r, DXGI_FORMAT f, int w, int h, ID3D11Texture2D **t, ID3D11ShaderResourceView **s) {
	D3D11_TEXTURE2D_DESC td;
	memset(&td, 0, sizeof td);
	td.Width = w; td.Height = h; td.MipLevels = 1; td.ArraySize = 1; td.Format = f;
	td.SampleDesc.Count = 1; td.Usage = D3D11_USAGE_DEFAULT; td.BindFlags = D3D11_BIND_SHADER_RESOURCE;
	HRESULT hr = ID3D11Device_CreateTexture2D(r->dev, &td, NULL, t);
	if (SUCCEEDED(hr)) hr = ID3D11Device_CreateShaderResourceView(r->dev, (ID3D11Resource *)*t, NULL, s);
	return hr;
}

// rr_upload_nv12 sobe um quadro NV12 contíguo (w x h) da memória para a
// visão v, em planos separados (Y em R8, UV em R8G8): funciona em qualquer
// placa, inclusive nas que não leem NV12 por shader.
int rr_upload_nv12(rr_ctx *r, int v, const uint8_t *nv12, int w, int h) {
	if (v < 0 || v > 1 || w < 2 || h < 2) return -1;
	if (!r->py[v] || r->vw[v] != w || r->vh[v] != h) {
		rr_drop_view(r, v);
		if (FAILED(rr_plane(r, DXGI_FORMAT_R8_UNORM, w, h, &r->py[v], &r->sy[v])) ||
		    FAILED(rr_plane(r, DXGI_FORMAT_R8G8_UNORM, w / 2, h / 2, &r->puv[v], &r->suv[v]))) {
			rr_drop_view(r, v);
			return -1;
		}
		r->vw[v] = w; r->vh[v] = h;
	}
	ID3D11DeviceContext_UpdateSubresource(r->ctx, (ID3D11Resource *)r->py[v], 0, NULL, nv12, w, 0);
	ID3D11DeviceContext_UpdateSubresource(r->ctx, (ID3D11Resource *)r->puv[v], 0, NULL, nv12 + (size_t)w * h, w, 0);
	return 0;
}

// rr_comp_size garante a composta em w x h; ao mudar de tamanho, ela volta
// preta (o próximo quadro é inteiro: keyframe).
static int rr_comp_size(rr_ctx *r, int w, int h) {
	if (r->comp && r->cw == w && r->ch == h) return 0;
	RR_REL(r->comp_srv); RR_REL(r->comp_rtv); RR_REL(r->comp);
	D3D11_TEXTURE2D_DESC td;
	memset(&td, 0, sizeof td);
	td.Width = w; td.Height = h; td.MipLevels = 1; td.ArraySize = 1; td.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
	td.SampleDesc.Count = 1; td.Usage = D3D11_USAGE_DEFAULT; td.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
	if (FAILED(ID3D11Device_CreateTexture2D(r->dev, &td, NULL, &r->comp)) ||
	    FAILED(ID3D11Device_CreateRenderTargetView(r->dev, (ID3D11Resource *)r->comp, NULL, &r->comp_rtv)) ||
	    FAILED(ID3D11Device_CreateShaderResourceView(r->dev, (ID3D11Resource *)r->comp, NULL, &r->comp_srv))) {
		RR_REL(r->comp_srv); RR_REL(r->comp_rtv); RR_REL(r->comp);
		return -1;
	}
	float black[4] = {0, 0, 0, 1};
	ID3D11DeviceContext_ClearRenderTargetView(r->ctx, r->comp_rtv, black);
	r->cw = w; r->ch = h;
	return 0;
}

static void rr_pass(rr_ctx *r, ID3D11PixelShader *ps, int slot, ID3D11ShaderResourceView **srv, int nsrv, ID3D11RenderTargetView *rt,
                    D3D11_VIEWPORT *vp, const D3D11_RECT *rects, int nrects) {
	ID3D11DeviceContext *x = r->ctx;
	ID3D11DeviceContext_IASetPrimitiveTopology(x, D3D11_PRIMITIVE_TOPOLOGY_TRIANGLELIST);
	ID3D11DeviceContext_IASetInputLayout(x, NULL);
	ID3D11DeviceContext_VSSetShader(x, r->vs, NULL, 0);
	ID3D11DeviceContext_RSSetState(x, r->rs);
	ID3D11DeviceContext_RSSetViewports(x, 1, vp);
	ID3D11DeviceContext_PSSetShader(x, ps, NULL, 0);
	ID3D11DeviceContext_PSSetConstantBuffers(x, 0, 1, &r->cb);
	ID3D11DeviceContext_PSSetSamplers(x, 0, 1, &r->lin);
	ID3D11DeviceContext_PSSetShaderResources(x, slot, nsrv, srv);
	ID3D11DeviceContext_OMSetRenderTargets(x, 1, &rt, NULL);
	// uma passada por retângulo: o rasterizador só pinta dentro da tesoura
	for (int i = 0; i < nrects; i++) {
		ID3D11DeviceContext_RSSetScissorRects(x, 1, &rects[i]);
		ID3D11DeviceContext_Draw(x, 3, 0);
	}
	ID3D11RenderTargetView *none = NULL;
	ID3D11ShaderResourceView *nosrv[4] = {NULL, NULL, NULL, NULL};
	ID3D11DeviceContext_OMSetRenderTargets(x, 1, &none, NULL);
	ID3D11DeviceContext_PSSetShaderResources(x, slot, nsrv, nosrv);
}

// rr_compose_video desenha o vídeo das visões na composta (w x h), só
// dentro dos retângulos rects (x, y, largura, altura); nrects < 0 = o quadro
// inteiro.
int rr_compose_video(rr_ctx *r, int kind, int w, int h, const int *rects, int nrects) {
	if (rr_comp_size(r, w, h) != 0) return -1;
	if (!r->sy[0] || r->vw[0] != w || r->vh[0] != h) return -1;
	if (kind == RR_AVC444 && (!r->sy[1] || r->vw[1] != w || r->vh[1] != h)) kind = RR_AVC444_M;
	rr_consts k;
	memset(&k, 0, sizeof k);
	k.info[0] = w; k.info[1] = h;
	ID3D11DeviceContext_UpdateSubresource(r->ctx, (ID3D11Resource *)r->cb, 0, NULL, &k, 0, 0);
	ID3D11ShaderResourceView *srv[4] = {r->sy[0], r->suv[0], r->sy[1], r->suv[1]};
	ID3D11PixelShader *ps = kind == RR_NV12 ? r->ps_nv12 : kind == RR_AVC444 ? r->ps_444 : r->ps_444m;
	D3D11_VIEWPORT vp = {0, 0, (FLOAT)w, (FLOAT)h, 0, 1};
	D3D11_RECT one = {0, 0, w, h};
	if (nrects < 0) {
		rr_pass(r, ps, 0, srv, kind == RR_AVC444 ? 4 : 2, r->comp_rtv, &vp, &one, 1);
		return 0;
	}
	D3D11_RECT *rs = nrects ? malloc(sizeof *rs * nrects) : NULL;
	int n = 0;
	for (int i = 0; i < nrects; i++) {
		LONG x0 = rects[4 * i], y0 = rects[4 * i + 1];
		LONG x1 = x0 + rects[4 * i + 2], y1 = y0 + rects[4 * i + 3];
		if (x0 < 0) x0 = 0;
		if (y0 < 0) y0 = 0;
		if (x1 > w) x1 = w;
		if (y1 > h) y1 = h;
		if (x1 > x0 && y1 > y0) { D3D11_RECT q = {x0, y0, x1, y1}; rs[n++] = q; }
	}
	if (n) rr_pass(r, ps, 0, srv, kind == RR_AVC444 ? 4 : 2, r->comp_rtv, &vp, rs, n);
	free(rs);
	return 0;
}

// rr_compose_bgra põe uma imagem BGRA (w x h) na composta em (x, y): um
// retalho sem perda, ou o quadro inteiro de JPEG/PNG. fw x fh é o tamanho do
// quadro (a composta é recriada se mudou).
int rr_compose_bgra(rr_ctx *r, int x, int y, int w, int h, const uint8_t *bgra, int fw, int fh) {
	if (rr_comp_size(r, fw, fh) != 0) return -1;
	if (x < 0 || y < 0 || w <= 0 || h <= 0 || x + w > fw || y + h > fh) return -1;
	D3D11_BOX box = {(UINT)x, (UINT)y, 0, (UINT)(x + w), (UINT)(y + h), 1};
	ID3D11DeviceContext_UpdateSubresource(r->ctx, (ID3D11Resource *)r->comp, 0, &box, bgra, w * 4, 0);
	return 0;
}

// rr_draw desenha a composta no retângulo de destino (dx, dy, dw, dh) do alvo
// rt de tamanho tw x th, com fundo bg (0xRRGGBB) no resto.
static void rr_draw(rr_ctx *r, ID3D11RenderTargetView *rt, int tw, int th, int dx, int dy, int dw, int dh, unsigned bg) {
	float c[4] = {((bg >> 16) & 255) / 255.0f, ((bg >> 8) & 255) / 255.0f, (bg & 255) / 255.0f, 1};
	ID3D11DeviceContext_ClearRenderTargetView(r->ctx, rt, c);
	if (!r->comp || dw <= 0 || dh <= 0) return;
	rr_consts k;
	memset(&k, 0, sizeof k);
	k.dst[0] = (float)dx; k.dst[1] = (float)dy; k.dst[2] = (float)dw; k.dst[3] = (float)dh;
	k.src[0] = (float)r->cw / dw; k.src[1] = (float)r->ch / dh;
	k.info[0] = r->cw; k.info[1] = r->ch;
	k.info[2] = (int)k.src[0] + 2; k.info[3] = (int)k.src[1] + 2;
	ID3D11DeviceContext_UpdateSubresource(r->ctx, (ID3D11Resource *)r->cb, 0, NULL, &k, 0, 0);
	D3D11_VIEWPORT vp = {0, 0, (FLOAT)tw, (FLOAT)th, 0, 1};
	LONG x0 = dx < 0 ? 0 : dx, y0 = dy < 0 ? 0 : dy;
	LONG x1 = dx + dw > tw ? tw : dx + dw, y1 = dy + dh > th ? th : dy + dh;
	if (x1 <= x0 || y1 <= y0) return;
	D3D11_RECT sr = {x0, y0, x1, y1};
	rr_pass(r, r->ps_present, 4, &r->comp_srv, 1, rt, &vp, &sr, 1);
}

// rr_present mostra a composta na janela, no retângulo (dx, dy, dw, dh).
int rr_present(rr_ctx *r, int dx, int dy, int dw, int dh, unsigned bg) {
	if (!r->sc || !r->bb) return -1;
	rr_draw(r, r->bb, r->bw, r->bh, dx, dy, dw, dh, bg);
	HRESULT hr = IDXGISwapChain_Present(r->sc, 0, 0);
	return FAILED(hr) ? -1 : 0;
}

// rr_selftest roda os mesmos shaders sem janela, para o teste
// (render_windows_test.go) comparar com as referências em Go/C, no Windows
// ou no Wine. Etapas:
//   1. sobe main (e aux, se não for NULL) como visões NV12 de w x h e
//      compõe o vídeo inteiro com kind;
//   2. põe o retalho BGRA patch (pw x ph) em (px, py);
//   3. compõe o vídeo de novo só no retângulo region (x, y, w, h);
//   4. desenha a composta num alvo de ow x oh (redução/ampliação).
// comp recebe a composta (w*h*4, BGRA) depois da etapa 3 e out o alvo final.
int rr_selftest(int kind, int w, int h, const uint8_t *main, const uint8_t *aux, const uint8_t *patch, int px, int py, int pw, int ph,
                const int *region, int ow, int oh, uint8_t *comp, uint8_t *out, char *err, int errlen) {
	rr_ctx *r = calloc(1, sizeof *r);
	ID3D11Texture2D *tgt = NULL, *stg = NULL;
	ID3D11RenderTargetView *rtv = NULL;
	int ret = -1;
	if (rr_device(r, 1, err, errlen) != 0 || rr_setup(r, err, errlen) != 0) goto done;
	if (rr_upload_nv12(r, 0, main, w, h) != 0 || (aux && rr_upload_nv12(r, 1, aux, w, h) != 0)) {
		snprintf(err, errlen, "upload NV12");
		goto done;
	}
	if (rr_compose_video(r, kind, w, h, NULL, -1) != 0) { snprintf(err, errlen, "compose 1"); goto done; }
	if (patch && rr_compose_bgra(r, px, py, pw, ph, patch, w, h) != 0) { snprintf(err, errlen, "retalho"); goto done; }
	// o vídeo de novo, só na região: fora dela o retalho tem de sobreviver
	if (region && rr_compose_video(r, kind, w, h, region, 1) != 0) { snprintf(err, errlen, "compose 2"); goto done; }
	D3D11_TEXTURE2D_DESC td;
	memset(&td, 0, sizeof td);
	td.MipLevels = 1; td.ArraySize = 1; td.Format = DXGI_FORMAT_B8G8R8A8_UNORM; td.SampleDesc.Count = 1;
	// lê a composta
	td.Width = w; td.Height = h; td.Usage = D3D11_USAGE_STAGING; td.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
	if (FAILED(ID3D11Device_CreateTexture2D(r->dev, &td, NULL, &stg))) goto done;
	ID3D11DeviceContext_CopyResource(r->ctx, (ID3D11Resource *)stg, (ID3D11Resource *)r->comp);
	D3D11_MAPPED_SUBRESOURCE m;
	if (FAILED(ID3D11DeviceContext_Map(r->ctx, (ID3D11Resource *)stg, 0, D3D11_MAP_READ, 0, &m))) goto done;
	for (int y = 0; y < h; y++) memcpy(comp + (size_t)y * w * 4, (uint8_t *)m.pData + (size_t)y * m.RowPitch, (size_t)w * 4);
	ID3D11DeviceContext_Unmap(r->ctx, (ID3D11Resource *)stg, 0);
	RR_REL(stg);
	// passe final num alvo do tamanho pedido
	td.Width = ow; td.Height = oh; td.Usage = D3D11_USAGE_DEFAULT; td.CPUAccessFlags = 0; td.BindFlags = D3D11_BIND_RENDER_TARGET;
	if (FAILED(ID3D11Device_CreateTexture2D(r->dev, &td, NULL, &tgt)) ||
	    FAILED(ID3D11Device_CreateRenderTargetView(r->dev, (ID3D11Resource *)tgt, NULL, &rtv))) goto done;
	rr_draw(r, rtv, ow, oh, 0, 0, ow, oh, 0);
	td.Usage = D3D11_USAGE_STAGING; td.CPUAccessFlags = D3D11_CPU_ACCESS_READ; td.BindFlags = 0;
	if (FAILED(ID3D11Device_CreateTexture2D(r->dev, &td, NULL, &stg))) goto done;
	ID3D11DeviceContext_CopyResource(r->ctx, (ID3D11Resource *)stg, (ID3D11Resource *)tgt);
	if (FAILED(ID3D11DeviceContext_Map(r->ctx, (ID3D11Resource *)stg, 0, D3D11_MAP_READ, 0, &m))) goto done;
	for (int y = 0; y < oh; y++) memcpy(out + (size_t)y * ow * 4, (uint8_t *)m.pData + (size_t)y * m.RowPitch, (size_t)ow * 4);
	ID3D11DeviceContext_Unmap(r->ctx, (ID3D11Resource *)stg, 0);
	ret = 0;
done:
	RR_REL(stg); RR_REL(rtv); RR_REL(tgt);
	rr_close(r);
	return ret;
}
