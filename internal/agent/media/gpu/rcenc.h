// rcenc.h - captura + codificação na GPU para Windows (incluído só por gpu_windows.go).
//
// Três modos, todos com a mesma captura (DXGI Desktop Duplication):
//
//   RC_H264   D3D11 Video Processor (escala + BGRA->NV12 BT.709 16-235)
//             -> Media Foundation H.264 (NVENC / Intel QSV / AMD AMF; reserva: software da Microsoft)
//   RC_AV1    o mesmo, com o encoder AV1 de hardware (sem reserva em software)
//   RC_AVC444 shaders D3D11 montam as duas visões NV12 do AVC444 (BT.709 0-255,
//             veja internal/avc444) -> DOIS encoders H.264 de hardware, um por visão
//
// A saída é Annex-B (H.264) ou OBUs (AV1), decodificada no navegador via
// WebCodecs ou no visualizador nativo via Media Foundation.
#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <d3d10.h>
#include <d3dcompiler.h>
#include <dxgi1_2.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mftransform.h>
#include <mferror.h>
#include <codecapi.h>
#include <wmcodecdsp.h>
#include <mmsystem.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Media Foundation carregado dinamicamente: em Windows "N" (sem Media Feature Pack) o
// exe continua abrindo e o agente cai para JPEG em vez de falhar ao iniciar.
typedef HRESULT (WINAPI *pfnMFStartup)(ULONG, DWORD);
typedef HRESULT (WINAPI *pfnMFShutdown)(void);
typedef HRESULT (WINAPI *pfnMFCreateMediaType)(IMFMediaType **);
typedef HRESULT (WINAPI *pfnMFCreateSample)(IMFSample **);
typedef HRESULT (WINAPI *pfnMFCreateMemoryBuffer)(DWORD, IMFMediaBuffer **);
typedef HRESULT (WINAPI *pfnMFCreateDXGISurfaceBuffer)(REFIID, IUnknown *, UINT, BOOL, IMFMediaBuffer **);
typedef HRESULT (WINAPI *pfnMFCreateDXGIDeviceManager)(UINT *, IMFDXGIDeviceManager **);
typedef HRESULT (WINAPI *pfnMFTEnumEx)(GUID, UINT32, const MFT_REGISTER_TYPE_INFO *, const MFT_REGISTER_TYPE_INFO *, IMFActivate ***, UINT32 *);
static pfnMFStartup pMFStartup;
static pfnMFShutdown pMFShutdown;
static pfnMFCreateMediaType pMFCreateMediaType;
static pfnMFCreateSample pMFCreateSample;
static pfnMFCreateMemoryBuffer pMFCreateMemoryBuffer;
static pfnMFCreateDXGISurfaceBuffer pMFCreateDXGISurfaceBuffer;
static pfnMFCreateDXGIDeviceManager pMFCreateDXGIDeviceManager;
static pfnMFTEnumEx pMFTEnumEx;

static int mf_load(void) {
	static int state = 0; // 0 = não tentou, 1 = ok, -1 = indisponível
	if (state) return state;
	HMODULE m = LoadLibraryW(L"mfplat.dll");
	state = -1;
	if (!m) return state;
#define LOADF(n) if (!(p##n = (pfn##n)(void *)GetProcAddress(m, #n))) return state;
	LOADF(MFStartup) LOADF(MFShutdown) LOADF(MFCreateMediaType) LOADF(MFCreateSample) LOADF(MFCreateMemoryBuffer)
	LOADF(MFCreateDXGISurfaceBuffer) LOADF(MFCreateDXGIDeviceManager) LOADF(MFTEnumEx)
#undef LOADF
	return state = 1;
}
#define MFStartup pMFStartup
#define MFShutdown pMFShutdown
#define MFCreateMediaType pMFCreateMediaType
#define MFCreateSample pMFCreateSample
#define MFCreateMemoryBuffer pMFCreateMemoryBuffer
#define MFCreateDXGISurfaceBuffer pMFCreateDXGISurfaceBuffer
#define MFCreateDXGIDeviceManager pMFCreateDXGIDeviceManager
#define MFTEnumEx pMFTEnumEx

// O compilador de shaders também é carregado na hora: vem com o Windows 10+
// (d3dcompiler_47.dll); sem ele, só o AVC444 fica indisponível.
typedef HRESULT (WINAPI *pfnD3DCompile)(const void *, SIZE_T, const char *, const D3D_SHADER_MACRO *, ID3DInclude *,
	const char *, const char *, UINT, UINT, ID3DBlob **, ID3DBlob **);
static pfnD3DCompile rcD3DCompile;
static int d3dc_load(void) {
	static int state = 0;
	if (state) return state;
	HMODULE m = LoadLibraryW(L"d3dcompiler_47.dll");
	state = -1;
	if (m && (rcD3DCompile = (pfnD3DCompile)(void *)GetProcAddress(m, "D3DCompile"))) state = 1;
	return state;
}

#define RC_FORCE_KEY 1
#define RC_REPEAT    2
#define RC_RING      8

#define RC_OK_FRAME   1
#define RC_NOTHING    0
#define RC_ERR       -1
#define RC_REOPEN    -2

#define RC_H264   0
#define RC_AVC444 1
#define RC_AV1    2

static const GUID rcLowLatency = {STATIC_CODECAPI_AVLowLatencyMode};
static const GUID rcRateCtl    = {STATIC_CODECAPI_AVEncCommonRateControlMode};
static const GUID rcMeanBR     = {STATIC_CODECAPI_AVEncCommonMeanBitRate};
static const GUID rcGOP        = {STATIC_CODECAPI_AVEncMPVGOPSize};
static const GUID rcBFrames    = {STATIC_CODECAPI_AVEncMPVDefaultBPictureCount};
static const GUID rcQvS        = {STATIC_CODECAPI_AVEncCommonQualityVsSpeed};
static const GUID rcForceKey   = {STATIC_CODECAPI_AVEncVideoForceKeyFrame};
// intra-refresh: reescreve a tela em faixas contínuas => limpa fantasmas sem pico de banda
static const GUID rcIRMode  = {0xb998208, 0xffc, 0x4a7f, {0x92, 0x5b, 0xdd, 0x4a, 0x74, 0x8b, 0xa, 0xfc}}; // AVEncVideoIntraRefreshMode
static const GUID rcBufSize = {STATIC_CODECAPI_AVEncCommonBufferSize};
static const GUID rcMaxBR   = {STATIC_CODECAPI_AVEncCommonMaxBitRate};
// não exportado pelo mingw: {1d39518c-e220-4da8-a07f-ba172552d6b1}
static const GUID rcEnumLUID   = {0x1d39518c, 0xe220, 0x4da8, {0xa0, 0x7f, 0xba, 0x17, 0x25, 0x52, 0xd6, 0xb1}};

// Um encoder do Media Foundation. O modo AVC444 usa dois.
typedef struct rc_enc {
	IMFActivate *act;
	IMFTransform *mft;
	ICodecAPI *api;
	IMFMediaEventGenerator *events;
	int hw, async_, d3d_in, provides;
	DWORD out_size;
	int need_input;
	int nenc; // quadros já enviados a este encoder
	int kbps;
	IMFSample *outsample;
	uint8_t *out;
	int out_len, out_cap, out_key;
	uint8_t *seqhdr; // MF_MT_MPEG_SEQUENCE_HEADER do tipo de saída (AV1), se houver
	int seqhdr_len;
	char name[160];
} rc_enc;

typedef struct rc_ctx {
	int mode;
	ID3D11Device *dev;
	ID3D11DeviceContext *ctx;
	ID3D11VideoDevice *vdev;
	ID3D11VideoContext *vctx;
	IDXGIOutputDuplication *dup;
	LUID luid;
	int left, top, dw, dh, ow, oh, fps, kbps;

	ID3D11Texture2D *bgra;  // tela capturada (sem cursor, entra no encoder)
	// RC_H264 / RC_AV1: video processor BGRA -> NV12
	ID3D11VideoProcessorEnumerator *vpe;
	ID3D11VideoProcessor *vp;
	ID3D11VideoProcessorInputView *vin;
	ID3D11VideoProcessorOutputView *vout[RC_RING];
	// RC_AVC444: shaders que escrevem nos planos das duas visões
	ID3D11ShaderResourceView *srv;
	ID3D11VertexShader *vs;
	ID3D11PixelShader *ps[4]; // Y principal, UV principal, Y auxiliar, UV auxiliar
	ID3D11RasterizerState *rs;
	ID3D11RenderTargetView *rtv[2][RC_RING][2]; // [visão][slot][plano]
	ID3D11Texture2D *nv12[2][RC_RING];          // [visão][slot]; visão 1 só no AVC444
	ID3D11Texture2D *staging;
	int ring, ringn, last, have_last;

	rc_enc enc[2];
	int nenc; // encoders em uso: 2 no AVC444, 1 nos outros
	IMFDXGIDeviceManager *mgr;
	UINT token;

	LARGE_INTEGER qpf, t0;
	char name[200];
	int com, mfs;
	HANDLE hrtimer; // timer de alta resolução para as esperas curtas (rc_nap)
	int tbp;        // pediu timeBeginPeriod(1) porque não há timer de alta resolução
} rc_ctx;

#define REL(p) do { if (p) { (p)->lpVtbl->Release(p); (p) = NULL; } } while (0)
#define CHK(x, msg) do { hr = (x); if (FAILED(hr)) { snprintf(err, errlen, "%s: 0x%08lx", msg, (unsigned long)hr); goto fail; } } while (0)

// rc_nap espera ~0,5 ms enquanto o encoder trabalha. Sleep(1) não serve: sem
// timeBeginPeriod ele dura ~15,6 ms (a resolução padrão do Windows), e o Go
// atual não muda mais essa resolução para o processo. Com duas ou quatro
// esperas por quadro, isso sozinho prendia a transmissão em 15-30 fps. O
// timer de alta resolução existe desde o Windows 10 1803; antes dele, o
// rc_open pede timeBeginPeriod(1) e o Sleep(1) passa a durar ~1 ms.
static void rc_nap(rc_ctx *c) {
	if (c->hrtimer) {
		LARGE_INTEGER due;
		due.QuadPart = -5000; // 0,5 ms, em unidades de 100 ns, relativo
		if (SetWaitableTimer(c->hrtimer, &due, 0, NULL, NULL, FALSE)) {
			WaitForSingleObject(c->hrtimer, 50);
			return;
		}
	}
	Sleep(1);
}

static HRESULT set_size(IMFMediaType *t, REFGUID k, UINT32 a, UINT32 b) {
	return IMFMediaType_SetUINT64(t, k, ((UINT64)a << 32) | b);
}

static void capi_u4(ICodecAPI *api, const GUID *g, ULONG v) {
	if (!api) return;
	VARIANT var; VariantInit(&var); var.vt = VT_UI4; var.ulVal = v;
	ICodecAPI_SetValue(api, g, &var);
}
static void capi_bool(ICodecAPI *api, const GUID *g, int v) {
	if (!api) return;
	VARIANT var; VariantInit(&var); var.vt = VT_BOOL; var.boolVal = v ? VARIANT_TRUE : VARIANT_FALSE;
	ICodecAPI_SetValue(api, g, &var);
}

static void close_encoder(rc_enc *e) {
	if (e->mft) {
		IMFTransform_ProcessMessage(e->mft, MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
		IMFTransform_ProcessMessage(e->mft, MFT_MESSAGE_SET_D3D_MANAGER, 0);
	}
	REL(e->outsample);
	REL(e->events);
	REL(e->api);
	REL(e->mft);
	if (e->act) { IMFActivate_ShutdownObject(e->act); REL(e->act); }
	e->need_input = 0;
	e->nenc = 0;
	free(e->seqhdr);
	e->seqhdr = NULL;
	e->seqhdr_len = 0;
}

static void free_encoder(rc_enc *e) {
	close_encoder(e);
	free(e->out);
	e->out = NULL;
	e->out_cap = e->out_len = 0;
}

static const GUID *rc_subtype(rc_ctx *c) { return c->mode == RC_AV1 ? &MFVideoFormat_AV1 : &MFVideoFormat_H264; }

// Configura um MFT já criado: tipos de mídia, baixa latência, sem B-frames, CBR.
static HRESULT configure_mft(rc_ctx *c, rc_enc *e, char *err, int errlen) {
	HRESULT hr;
	IMFAttributes *attrs = NULL;
	IMFMediaType *ot = NULL, *it = NULL;
	UINT32 v = 0;

	e->async_ = 0; e->d3d_in = 0;
	if (SUCCEEDED(IMFTransform_GetAttributes(e->mft, &attrs)) && attrs) {
		if (SUCCEEDED(IMFAttributes_GetUINT32(attrs, &MF_TRANSFORM_ASYNC, &v)) && v) {
			e->async_ = 1;
			IMFAttributes_SetUINT32(attrs, &MF_TRANSFORM_ASYNC_UNLOCK, TRUE);
		}
		v = 0;
		if (e->hw && SUCCEEDED(IMFAttributes_GetUINT32(attrs, &MF_SA_D3D11_AWARE, &v)) && v) e->d3d_in = 1;
		REL(attrs);
	}
	if (e->hw && !e->d3d_in) { snprintf(err, errlen, "MFT de hardware sem suporte D3D11"); return E_FAIL; }
	if (e->d3d_in)
		CHK(IMFTransform_ProcessMessage(e->mft, MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)c->mgr), "SET_D3D_MANAGER");

	IMFTransform_QueryInterface(e->mft, &IID_ICodecAPI, (void **)&e->api);
	capi_bool(e->api, &rcLowLatency, 1);
	capi_u4(e->api, &rcRateCtl, eAVEncCommonRateControlMode_CBR);
	capi_u4(e->api, &rcMeanBR, (ULONG)e->kbps * 1000);
	capi_u4(e->api, &rcGOP, (ULONG)c->fps);         // keyframe a cada 1s
	capi_u4(e->api, &rcIRMode, 0);                  // sem intra-refresh: com bitrate alto não precisa
	capi_u4(e->api, &rcBufSize, (ULONG)e->kbps * 125); // buffer VBV = 1 frame (~8ms) → latência mínima
	capi_u4(e->api, &rcMaxBR, (ULONG)e->kbps * 1500); // permite rajadas de até 50% nos keyframes
	capi_u4(e->api, &rcBFrames, 0);
	capi_u4(e->api, &rcQvS, 100); // 100 = prioriza velocidade (menor latência)

	// tipo de saída primeiro (regra de encoders MF). H.264: tenta High ->
	// Main -> Baseline. AV1: sem perfil (o encoder usa Main 8 bits) e, se
	// recusar, Main explícito (eAVEncAV1VProfile_Main_420_8 = 1).
	static const UINT32 h264p[] = {eAVEncH264VProfile_High, eAVEncH264VProfile_Main, eAVEncH264VProfile_Base};
	static const UINT32 av1p[] = {0, 1};
	const UINT32 *profiles = c->mode == RC_AV1 ? av1p : h264p;
	int nprof = c->mode == RC_AV1 ? 2 : 3;
	hr = E_FAIL;
	for (int i = 0; i < nprof && FAILED(hr); i++) {
		REL(ot);
		CHK(MFCreateMediaType(&ot), "MFCreateMediaType");
		IMFMediaType_SetGUID(ot, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
		IMFMediaType_SetGUID(ot, &MF_MT_SUBTYPE, rc_subtype(c));
		IMFMediaType_SetUINT32(ot, &MF_MT_AVG_BITRATE, (UINT32)e->kbps * 1000);
		set_size(ot, &MF_MT_FRAME_SIZE, c->ow, c->oh);
		set_size(ot, &MF_MT_FRAME_RATE, c->fps, 1);
		set_size(ot, &MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
		IMFMediaType_SetUINT32(ot, &MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
		if (profiles[i]) IMFMediaType_SetUINT32(ot, &MF_MT_MPEG2_PROFILE, profiles[i]);
		hr = IMFTransform_SetOutputType(e->mft, 0, ot, 0);
	}
	CHK(hr, "SetOutputType");

	CHK(MFCreateMediaType(&it), "MFCreateMediaType");
	IMFMediaType_SetGUID(it, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
	IMFMediaType_SetGUID(it, &MF_MT_SUBTYPE, &MFVideoFormat_NV12);
	set_size(it, &MF_MT_FRAME_SIZE, c->ow, c->oh);
	set_size(it, &MF_MT_FRAME_RATE, c->fps, 1);
	set_size(it, &MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
	IMFMediaType_SetUINT32(it, &MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
	IMFMediaType_SetUINT32(it, &MF_MT_YUV_MATRIX, MFVideoTransferMatrix_BT709);
	// AVC444 usa a faixa completa: o encoder não converte nada, só registra
	IMFMediaType_SetUINT32(it, &MF_MT_VIDEO_NOMINAL_RANGE, c->mode == RC_AVC444 ? MFNominalRange_0_255 : MFNominalRange_16_235);
	CHK(IMFTransform_SetInputType(e->mft, 0, it, 0), "SetInputType");

	MFT_OUTPUT_STREAM_INFO si;
	memset(&si, 0, sizeof si);
	CHK(IMFTransform_GetOutputStreamInfo(e->mft, 0, &si), "GetOutputStreamInfo");
	e->provides = (si.dwFlags & MFT_OUTPUT_STREAM_PROVIDES_SAMPLES) != 0;
	e->out_size = si.cbSize ? si.cbSize : (DWORD)(c->ow * c->oh * 2);

	// AV1: alguns encoders mandam o cabeçalho de sequência só no tipo de
	// saída, não no fluxo. O lado Go o põe de volta nos keyframes.
	if (c->mode == RC_AV1) {
		IMFMediaType *cur = NULL;
		UINT32 n = 0;
		if (SUCCEEDED(IMFTransform_GetOutputCurrentType(e->mft, 0, &cur)) &&
		    SUCCEEDED(IMFMediaType_GetBlobSize(cur, &MF_MT_MPEG_SEQUENCE_HEADER, &n)) && n > 0 && n < 4096) {
			e->seqhdr = malloc(n);
			if (e->seqhdr && SUCCEEDED(IMFMediaType_GetBlob(cur, &MF_MT_MPEG_SEQUENCE_HEADER, e->seqhdr, n, &n)))
				e->seqhdr_len = (int)n;
		}
		REL(cur);
	}

	if (e->async_)
		CHK(IMFTransform_QueryInterface(e->mft, &IID_IMFMediaEventGenerator, (void **)&e->events), "EventGenerator");

	IMFTransform_ProcessMessage(e->mft, MFT_MESSAGE_COMMAND_FLUSH, 0);
	CHK(IMFTransform_ProcessMessage(e->mft, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0), "BEGIN_STREAMING");
	CHK(IMFTransform_ProcessMessage(e->mft, MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0), "START_OF_STREAM");
	hr = S_OK;
fail:
	REL(ot);
	REL(it);
	return hr;
}

// open_encoder abre o encoder de hardware da GPU que tem o desktop. Sem ele,
// só o H.264 comum tem reserva (o encoder software da Microsoft): AV1 e
// AVC444 em software pesariam demais no PC atendido.
static int open_encoder(rc_ctx *c, rc_enc *e, int allow_hw, char *err, int errlen) {
	HRESULT hr;
	char last[200] = "nenhum encoder de GPU";
	if (allow_hw) {
		MFT_REGISTER_TYPE_INFO in = {MFMediaType_Video, MFVideoFormat_NV12};
		MFT_REGISTER_TYPE_INFO out = {MFMediaType_Video, *rc_subtype(c)};
		IMFActivate **acts = NULL;
		UINT32 n = 0;
		if (SUCCEEDED(MFTEnumEx(MFT_CATEGORY_VIDEO_ENCODER, MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER,
		                        &in, &out, &acts, &n))) {
			for (UINT32 i = 0; i < n; i++) {
				IMFActivate *a = acts[i];
				acts[i] = NULL;
				if (e->mft) { REL(a); continue; }
				// em PCs com 2 GPUs, usa só o encoder da GPU que tem o desktop
				LUID l; UINT32 bl = 0; UINT64 l64 = 0;
				UINT64 mine = ((UINT64)(UINT32)c->luid.HighPart << 32) | c->luid.LowPart;
				if (SUCCEEDED(IMFActivate_GetBlob(a, &rcEnumLUID, (UINT8 *)&l, sizeof l, &bl)) && bl == sizeof l) {
					if (l.LowPart != c->luid.LowPart || l.HighPart != c->luid.HighPart) { REL(a); continue; }
				} else if (SUCCEEDED(IMFActivate_GetUINT64(a, &rcEnumLUID, &l64)) && l64 != mine) { REL(a); continue; }
				e->hw = 1;
				if (FAILED(IMFActivate_ActivateObject(a, &IID_IMFTransform, (void **)&e->mft))) { REL(a); continue; }
				e->act = a;
				WCHAR *wn = NULL; UINT32 wl = 0;
				if (SUCCEEDED(IMFActivate_GetAllocatedString(a, &MFT_FRIENDLY_NAME_Attribute, &wn, &wl))) {
					WideCharToMultiByte(CP_UTF8, 0, wn, -1, e->name, sizeof e->name, NULL, NULL);
					CoTaskMemFree(wn);
				}
				if (FAILED(configure_mft(c, e, last, sizeof last))) close_encoder(e);
			}
			CoTaskMemFree(acts);
		}
		if (e->mft) return 0;
	}
	if (c->mode != RC_H264) {
		snprintf(err, errlen, "%s: %s", c->mode == RC_AV1 ? "AV1 sem encoder de GPU" : "AVC444 sem encoder de GPU", last);
		return -1;
	}
	e->hw = 0;
	snprintf(e->name, sizeof e->name, "Microsoft H.264 (software)");
	CHK(CoCreateInstance(&CLSID_CMSH264EncoderMFT, NULL, CLSCTX_INPROC_SERVER, &IID_IMFTransform, (void **)&e->mft),
	    "encoder software");
	CHK(configure_mft(c, e, err, errlen), "configurar encoder software");
	return 0;
fail:
	close_encoder(e);
	return -1;
}

void rc_close(rc_ctx *c) {
	if (!c) return;
	for (int i = 0; i < 2; i++) free_encoder(&c->enc[i]);
	REL(c->staging);
	for (int v = 0; v < 2; v++)
		for (int i = 0; i < RC_RING; i++) {
			REL(c->rtv[v][i][0]);
			REL(c->rtv[v][i][1]);
			REL(c->nv12[v][i]);
		}
	for (int i = 0; i < RC_RING; i++) REL(c->vout[i]);
	for (int i = 0; i < 4; i++) REL(c->ps[i]);
	REL(c->vs);
	REL(c->rs);
	REL(c->srv);
	REL(c->vin);
	REL(c->vp);
	REL(c->vpe);
	REL(c->bgra);
	REL(c->dup);
	REL(c->mgr);
	REL(c->vctx);
	REL(c->vdev);
	REL(c->ctx);
	REL(c->dev);
	if (c->mfs) MFShutdown();
	if (c->com) CoUninitialize();
	if (c->hrtimer) CloseHandle(c->hrtimer);
	if (c->tbp) timeEndPeriod(1);
	free(c);
}

const char *rc_name(rc_ctx *c) { return c->name; }
int rc_is_hw(rc_ctx *c) { return c->enc[0].hw; }
int rc_width(rc_ctx *c) { return c->ow; }
int rc_height(rc_ctx *c) { return c->oh; }

// No AVC444, a visão principal leva 60% do bitrate: tem o brilho, que é o
// que mais muda. A auxiliar só tem cor, que em tela de computador comprime bem.
static int enc_kbps(rc_ctx *c, int i) {
	if (c->nenc == 1) return c->kbps;
	return i == 0 ? c->kbps * 3 / 5 : c->kbps - c->kbps * 3 / 5;
}

void rc_set_bitrate(rc_ctx *c, int kbps) {
	c->kbps = kbps;
	for (int i = 0; i < c->nenc; i++) {
		c->enc[i].kbps = enc_kbps(c, i);
		capi_u4(c->enc[i].api, &rcMeanBR, (ULONG)c->enc[i].kbps * 1000);
	}
}

// Shaders do AVC444. A fórmula é a de internal/avc444 (ToYUV e Pack): mude
// os dois juntos. HALF é a metade da altura, definida na compilação.
static const char rc_hlsl[] =
	"Texture2D<float4> src : register(t0);\n"
	"static const float3 KY = float3(0.2126, 0.7152, 0.0722);\n"
	"float3 px(int x, int y) { return src.Load(int3(x, y, 0)).rgb * 255.0; }\n"
	"float2 uv(float3 c) { float y = dot(c, KY); return float2((c.b - y) / 1.8556 + 128.0, (c.r - y) / 1.5748 + 128.0); }\n"
	"void vs(uint id : SV_VertexID, out float4 pos : SV_Position) {\n"
	"  float2 t = float2((id << 1) & 2, id & 2);\n"
	"  pos = float4(t * float2(2, -2) + float2(-1, 1), 0, 1);\n"
	"}\n"
	// principal: Y de todos os pixels; U e V dos pixels (par, par)
	"float main_y(float4 p : SV_Position) : SV_Target { return dot(px((int)p.x, (int)p.y), KY) / 255.0; }\n"
	"float2 main_uv(float4 p : SV_Position) : SV_Target { return uv(px(2 * (int)p.x, 2 * (int)p.y)) / 255.0; }\n"
	// auxiliar: U das linhas ímpares em cima, V embaixo; UV das colunas ímpares das linhas pares
	"float aux_y(float4 p : SV_Position) : SV_Target {\n"
	"  int x = (int)p.x, y = (int)p.y;\n"
	"  if (y < HALF) return uv(px(x, 2 * y + 1)).x / 255.0;\n"
	"  return uv(px(x, 2 * (y - HALF) + 1)).y / 255.0;\n"
	"}\n"
	"float2 aux_uv(float4 p : SV_Position) : SV_Target { return uv(px(2 * (int)p.x + 1, 2 * (int)p.y)) / 255.0; }\n";

static HRESULT compile(const char *entry, const char *target, const D3D_SHADER_MACRO *defs, ID3DBlob **code,
                       char *err, int errlen) {
	ID3DBlob *msg = NULL;
	HRESULT hr = rcD3DCompile(rc_hlsl, sizeof rc_hlsl - 1, "avc444.hlsl", defs, NULL, entry, target,
	                         D3DCOMPILE_OPTIMIZATION_LEVEL3, 0, code, &msg);
	if (FAILED(hr))
		snprintf(err, errlen, "shader %s: 0x%08lx %s", entry, (unsigned long)hr,
		         msg ? (const char *)ID3D10Blob_GetBufferPointer(msg) : "");
	REL(msg);
	return hr;
}

// create_shaders compila os shaders do AVC444 para a altura c->oh e prepara
// a leitura da textura c->bgra.
static int create_shaders(rc_ctx *c, char *err, int errlen) {
	HRESULT hr;
	ID3DBlob *code = NULL;
	if (d3dc_load() != 1) { snprintf(err, errlen, "d3dcompiler_47.dll indisponível"); return -1; }
	char half[16];
	snprintf(half, sizeof half, "%d", c->oh / 2);
	D3D_SHADER_MACRO defs[] = {{"HALF", half}, {NULL, NULL}};
	CHK(compile("vs", "vs_4_0", defs, &code, err, errlen), "compilar vs");
	CHK(ID3D11Device_CreateVertexShader(c->dev, ID3D10Blob_GetBufferPointer(code), ID3D10Blob_GetBufferSize(code), NULL, &c->vs), "CreateVertexShader");
	REL(code);
	static const char *entries[4] = {"main_y", "main_uv", "aux_y", "aux_uv"};
	for (int i = 0; i < 4; i++) {
		CHK(compile(entries[i], "ps_4_0", defs, &code, err, errlen), "compilar ps");
		CHK(ID3D11Device_CreatePixelShader(c->dev, ID3D10Blob_GetBufferPointer(code), ID3D10Blob_GetBufferSize(code), NULL, &c->ps[i]), "CreatePixelShader");
		REL(code);
	}
	D3D11_RASTERIZER_DESC rd;
	memset(&rd, 0, sizeof rd);
	rd.FillMode = D3D11_FILL_SOLID;
	rd.CullMode = D3D11_CULL_NONE;
	rd.DepthClipEnable = TRUE;
	CHK(ID3D11Device_CreateRasterizerState(c->dev, &rd, &c->rs), "RasterizerState");
	D3D11_SHADER_RESOURCE_VIEW_DESC sd;
	memset(&sd, 0, sizeof sd);
	sd.Format = DXGI_FORMAT_B8G8R8A8_UNORM;
	sd.ViewDimension = D3D11_SRV_DIMENSION_TEXTURE2D;
	sd.Texture2D.MipLevels = 1;
	CHK(ID3D11Device_CreateShaderResourceView(c->dev, (ID3D11Resource *)c->bgra, &sd, &c->srv), "SRV da tela");
	return 0;
fail:
	REL(code);
	return -1;
}

// setup_avc444 prepara os shaders e as visões de render target nos planos
// NV12 (R8 = Y, R8G8 = UV), como o OBS faz para converter a tela em NV12.
static int setup_avc444(rc_ctx *c, char *err, int errlen) {
	HRESULT hr;
	UINT sup = 0;
	if (FAILED(ID3D11Device_CheckFormatSupport(c->dev, DXGI_FORMAT_NV12, &sup)) || !(sup & D3D11_FORMAT_SUPPORT_RENDER_TARGET)) {
		snprintf(err, errlen, "GPU não desenha em NV12");
		return -1;
	}
	if (create_shaders(c, err, errlen) != 0) return -1;
	D3D11_RENDER_TARGET_VIEW_DESC rv;
	memset(&rv, 0, sizeof rv);
	rv.ViewDimension = D3D11_RTV_DIMENSION_TEXTURE2D;
	for (int v = 0; v < 2; v++)
		for (int i = 0; i < c->ringn; i++) {
			rv.Format = DXGI_FORMAT_R8_UNORM;
			CHK(ID3D11Device_CreateRenderTargetView(c->dev, (ID3D11Resource *)c->nv12[v][i], &rv, &c->rtv[v][i][0]), "RTV Y");
			rv.Format = DXGI_FORMAT_R8G8_UNORM;
			CHK(ID3D11Device_CreateRenderTargetView(c->dev, (ID3D11Resource *)c->nv12[v][i], &rv, &c->rtv[v][i][1]), "RTV UV");
		}
	return 0;
fail:
	return -1;
}

// draw_passes roda as 4 passadas do AVC444, uma por plano, nos alvos rtv
// (Y principal, UV principal, Y auxiliar, UV auxiliar): um triângulo que
// cobre o alvo inteiro.
static void draw_passes(rc_ctx *c, ID3D11RenderTargetView *rtv[4]) {
	ID3D11DeviceContext *x = c->ctx;
	ID3D11DeviceContext_IASetPrimitiveTopology(x, D3D11_PRIMITIVE_TOPOLOGY_TRIANGLELIST);
	ID3D11DeviceContext_IASetInputLayout(x, NULL);
	ID3D11DeviceContext_VSSetShader(x, c->vs, NULL, 0);
	ID3D11DeviceContext_RSSetState(x, c->rs);
	ID3D11DeviceContext_PSSetShaderResources(x, 0, 1, &c->srv);
	for (int pass = 0; pass < 4; pass++) {
		int plane = pass % 2;
		D3D11_VIEWPORT vp = {0, 0, (FLOAT)(plane ? c->ow / 2 : c->ow), (FLOAT)(plane ? c->oh / 2 : c->oh), 0, 1};
		ID3D11DeviceContext_OMSetRenderTargets(x, 1, &rtv[pass], NULL);
		ID3D11DeviceContext_RSSetViewports(x, 1, &vp);
		ID3D11DeviceContext_PSSetShader(x, c->ps[pass], NULL, 0);
		ID3D11DeviceContext_Draw(x, 3, 0);
	}
	// solta os alvos: o encoder vai ler essas texturas
	ID3D11RenderTargetView *none = NULL;
	ID3D11ShaderResourceView *nosrv = NULL;
	ID3D11DeviceContext_OMSetRenderTargets(x, 1, &none, NULL);
	ID3D11DeviceContext_PSSetShaderResources(x, 0, 1, &nosrv);
}

// draw_avc444 escreve as duas visões do quadro capturado no slot.
static void draw_avc444(rc_ctx *c, int slot) {
	ID3D11RenderTargetView *rtv[4] = {c->rtv[0][slot][0], c->rtv[0][slot][1], c->rtv[1][slot][0], c->rtv[1][slot][1]};
	draw_passes(c, rtv);
}

// rc_avc444_selftest roda os shaders do AVC444 sobre a imagem bgra (w x h,
// pares) em texturas comuns R8/R8G8 e copia o resultado para out: visão
// principal e depois a auxiliar, as duas em NV12 contíguo (2 * w*h*3/2
// bytes). É o mesmo código do agente, menos o desenho direto nos planos NV12:
// o teste (avc444_windows_test.go) compara com internal/avc444.Pack, no
// Windows ou no Wine.
int rc_avc444_selftest(int w, int h, const uint8_t *bgra, uint8_t *out, char *err, int errlen) {
	HRESULT hr;
	rc_ctx *c = calloc(1, sizeof *c);
	ID3D11Texture2D *tex[4] = {0}, *stg[4] = {0};
	ID3D11RenderTargetView *rtv[4] = {0};
	int ret = -1;
	c->ow = w; c->oh = h;
	D3D_FEATURE_LEVEL lv[] = {D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1, D3D_FEATURE_LEVEL_10_0};
	hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, D3D11_CREATE_DEVICE_BGRA_SUPPORT, lv, 3, D3D11_SDK_VERSION, &c->dev, NULL, &c->ctx);
	if (FAILED(hr)) // sem GPU: o rasterizador em software do Windows
		hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_WARP, NULL, D3D11_CREATE_DEVICE_BGRA_SUPPORT, lv, 3, D3D11_SDK_VERSION, &c->dev, NULL, &c->ctx);
	CHK(hr, "D3D11CreateDevice");
	D3D11_TEXTURE2D_DESC td;
	memset(&td, 0, sizeof td);
	td.Width = w; td.Height = h; td.MipLevels = 1; td.ArraySize = 1;
	td.Format = DXGI_FORMAT_B8G8R8A8_UNORM; td.SampleDesc.Count = 1; td.Usage = D3D11_USAGE_DEFAULT;
	td.BindFlags = D3D11_BIND_SHADER_RESOURCE;
	D3D11_SUBRESOURCE_DATA init = {bgra, (UINT)w * 4, 0};
	CHK(ID3D11Device_CreateTexture2D(c->dev, &td, &init, &c->bgra), "textura de entrada");
	if (create_shaders(c, err, errlen) != 0) goto done;
	for (int i = 0; i < 4; i++) {
		int uv = i % 2;
		td.Width = uv ? w / 2 : w; td.Height = uv ? h / 2 : h;
		td.Format = uv ? DXGI_FORMAT_R8G8_UNORM : DXGI_FORMAT_R8_UNORM;
		td.Usage = D3D11_USAGE_DEFAULT; td.BindFlags = D3D11_BIND_RENDER_TARGET; td.CPUAccessFlags = 0;
		CHK(ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &tex[i]), "textura de saída");
		CHK(ID3D11Device_CreateRenderTargetView(c->dev, (ID3D11Resource *)tex[i], NULL, &rtv[i]), "RTV de saída");
		td.Usage = D3D11_USAGE_STAGING; td.BindFlags = 0; td.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
		CHK(ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &stg[i]), "textura de leitura");
	}
	draw_passes(c, rtv);
	uint8_t *dst = out;
	for (int i = 0; i < 4; i++) {
		int uv = i % 2, rows = uv ? h / 2 : h; // linha: w bytes nos dois planos
		D3D11_MAPPED_SUBRESOURCE m;
		ID3D11DeviceContext_CopyResource(c->ctx, (ID3D11Resource *)stg[i], (ID3D11Resource *)tex[i]);
		CHK(ID3D11DeviceContext_Map(c->ctx, (ID3D11Resource *)stg[i], 0, D3D11_MAP_READ, 0, &m), "Map");
		for (int y = 0; y < rows; y++) memcpy(dst + (size_t)y * w, (uint8_t *)m.pData + (size_t)y * m.RowPitch, w);
		ID3D11DeviceContext_Unmap(c->ctx, (ID3D11Resource *)stg[i], 0);
		dst += (size_t)rows * w;
	}
	ret = 0;
	goto done;
fail:
done:
	for (int i = 0; i < 4; i++) { REL(rtv[i]); REL(tex[i]); REL(stg[i]); }
	rc_close(c);
	return ret;
}

int rc_open(rc_ctx **outp, int mode, int maxw, int fps, int kbps, int allow_hw, char *err, int errlen) {
	HRESULT hr;
	rc_ctx *c = calloc(1, sizeof *c);
	IDXGIDevice *xdev = NULL;
	IDXGIAdapter *adp = NULL;
	IDXGIOutput *out = NULL;
	IDXGIOutput1 *out1 = NULL;
	ID3D10Multithread *mt = NULL;
	*outp = NULL;
	c->mode = mode;
	c->fps = fps;
	c->kbps = kbps;
	c->nenc = mode == RC_AVC444 ? 2 : 1;
	c->hrtimer = CreateWaitableTimerExW(NULL, NULL, CREATE_WAITABLE_TIMER_HIGH_RESOLUTION, TIMER_ALL_ACCESS);
	if (!c->hrtimer) c->tbp = timeBeginPeriod(1) == TIMERR_NOERROR;
	// cada visão do AVC444 tem o próprio anel: 4 slots bastam para o encoder
	// assíncrono e economizam memória de vídeo (são 2 texturas por slot)
	c->ringn = mode == RC_AVC444 ? 4 : RC_RING;
	if (mode == RC_AVC444) maxw = 0; // 4:4:4 só faz sentido na resolução nativa

	if (mf_load() != 1) {
		snprintf(err, errlen, "Media Foundation indisponível (Windows N?)");
		free(c);
		return RC_ERR;
	}
	hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
	c->com = SUCCEEDED(hr);
	CHK(MFStartup(MF_VERSION, MFSTARTUP_LITE), "MFStartup");
	c->mfs = 1;

	D3D_FEATURE_LEVEL lv[] = {D3D_FEATURE_LEVEL_11_1, D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1, D3D_FEATURE_LEVEL_10_0};
	UINT fl = D3D11_CREATE_DEVICE_BGRA_SUPPORT | D3D11_CREATE_DEVICE_VIDEO_SUPPORT;
	hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, fl, lv, 4, D3D11_SDK_VERSION, &c->dev, NULL, &c->ctx);
	if (hr == E_INVALIDARG)
		hr = D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, fl, lv + 1, 3, D3D11_SDK_VERSION, &c->dev, NULL, &c->ctx);
	CHK(hr, "D3D11CreateDevice");
	if (SUCCEEDED(ID3D11Device_QueryInterface(c->dev, &IID_ID3D10Multithread, (void **)&mt))) {
		ID3D10Multithread_SetMultithreadProtected(mt, TRUE);
		REL(mt);
	}

	// Desktop Duplication do monitor principal
	CHK(ID3D11Device_QueryInterface(c->dev, &IID_IDXGIDevice, (void **)&xdev), "IDXGIDevice");
	CHK(IDXGIDevice_GetAdapter(xdev, &adp), "GetAdapter");
	DXGI_ADAPTER_DESC ad;
	IDXGIAdapter_GetDesc(adp, &ad);
	c->luid = ad.AdapterLuid;
	CHK(IDXGIAdapter_EnumOutputs(adp, 0, &out), "EnumOutputs");
	CHK(IDXGIOutput_QueryInterface(out, &IID_IDXGIOutput1, (void **)&out1), "IDXGIOutput1");
	hr = IDXGIOutput1_DuplicateOutput(out1, (IUnknown *)c->dev, &c->dup);
	if (hr == E_ACCESSDENIED || hr == DXGI_ERROR_SESSION_DISCONNECTED) {
		snprintf(err, errlen, "tela protegida (UAC/bloqueio): 0x%08lx", (unsigned long)hr);
		rc_close(c); REL(out1); REL(out); REL(adp); REL(xdev);
		return RC_REOPEN;
	}
	CHK(hr, "DuplicateOutput");
	DXGI_OUTDUPL_DESC dd;
	IDXGIOutputDuplication_GetDesc(c->dup, &dd);
	DXGI_OUTPUT_DESC od;
	IDXGIOutput_GetDesc(out, &od);
	c->left = od.DesktopCoordinates.left;
	c->top = od.DesktopCoordinates.top;
	c->dw = dd.ModeDesc.Width;
	c->dh = dd.ModeDesc.Height;
	c->ow = c->dw; c->oh = c->dh;
	if (maxw > 0 && c->ow > maxw) { c->ow = maxw; c->oh = (int)((int64_t)c->dh * maxw / c->dw); }
	c->ow &= ~1; c->oh &= ~1;

	D3D11_TEXTURE2D_DESC td;
	memset(&td, 0, sizeof td);
	td.Width = c->dw; td.Height = c->dh; td.MipLevels = 1; td.ArraySize = 1;
	td.Format = DXGI_FORMAT_B8G8R8A8_UNORM; td.SampleDesc.Count = 1; td.Usage = D3D11_USAGE_DEFAULT;
	td.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_SHADER_RESOURCE;
	td.MiscFlags = 0;
	CHK(ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->bgra), "textura BGRA");

	// texturas NV12 que entram no encoder: 1 visão, ou 2 no AVC444
	td.Width = c->ow; td.Height = c->oh; td.Format = DXGI_FORMAT_NV12;
	td.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_VIDEO_ENCODER; td.MiscFlags = 0;
	for (int v = 0; v < c->nenc; v++)
		for (int i = 0; i < c->ringn; i++) {
			hr = ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->nv12[v][i]);
			if (FAILED(hr) && (td.BindFlags & D3D11_BIND_VIDEO_ENCODER)) {
				td.BindFlags = D3D11_BIND_RENDER_TARGET; // runtime antigo
				hr = ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->nv12[v][i]);
			}
			CHK(hr, "textura NV12");
		}
	td.BindFlags = 0; td.Usage = D3D11_USAGE_STAGING; td.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
	CHK(ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->staging), "textura staging");

	if (mode == RC_AVC444) {
		if (setup_avc444(c, err, errlen) != 0) goto fail_noset;
	} else {
		// Video processor: escala + conversão de cor na GPU
		CHK(ID3D11Device_QueryInterface(c->dev, &IID_ID3D11VideoDevice, (void **)&c->vdev), "ID3D11VideoDevice");
		CHK(ID3D11DeviceContext_QueryInterface(c->ctx, &IID_ID3D11VideoContext, (void **)&c->vctx), "ID3D11VideoContext");
		D3D11_VIDEO_PROCESSOR_CONTENT_DESC cd;
		memset(&cd, 0, sizeof cd);
		cd.InputFrameFormat = D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE;
		cd.InputFrameRate.Numerator = fps; cd.InputFrameRate.Denominator = 1;
		cd.InputWidth = c->dw; cd.InputHeight = c->dh;
		cd.OutputFrameRate.Numerator = fps; cd.OutputFrameRate.Denominator = 1;
		cd.OutputWidth = c->ow; cd.OutputHeight = c->oh;
		cd.Usage = D3D11_VIDEO_USAGE_OPTIMAL_SPEED;
		CHK(ID3D11VideoDevice_CreateVideoProcessorEnumerator(c->vdev, &cd, &c->vpe), "VideoProcessorEnumerator");
		CHK(ID3D11VideoDevice_CreateVideoProcessor(c->vdev, c->vpe, 0, &c->vp), "VideoProcessor");
		D3D11_VIDEO_PROCESSOR_INPUT_VIEW_DESC ivd;
		memset(&ivd, 0, sizeof ivd);
		ivd.ViewDimension = D3D11_VPIV_DIMENSION_TEXTURE2D;
		CHK(ID3D11VideoDevice_CreateVideoProcessorInputView(c->vdev, (ID3D11Resource *)c->bgra, c->vpe, &ivd, &c->vin), "InputView");
		D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC ovd;
		memset(&ovd, 0, sizeof ovd);
		ovd.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
		for (int i = 0; i < c->ringn; i++)
			CHK(ID3D11VideoDevice_CreateVideoProcessorOutputView(c->vdev, (ID3D11Resource *)c->nv12[0][i], c->vpe, &ovd, &c->vout[i]), "OutputView");

		D3D11_VIDEO_PROCESSOR_COLOR_SPACE ics, ocs;
		memset(&ics, 0, sizeof ics);
		memset(&ocs, 0, sizeof ocs);
		ics.RGB_Range = 0; // RGB 0-255
		ocs.YCbCr_Matrix = 1; // BT.709
		ocs.Nominal_Range = D3D11_VIDEO_PROCESSOR_NOMINAL_RANGE_16_235;
		ID3D11VideoContext_VideoProcessorSetStreamColorSpace(c->vctx, c->vp, 0, &ics);
		ID3D11VideoContext_VideoProcessorSetOutputColorSpace(c->vctx, c->vp, &ocs);
		ID3D11VideoContext_VideoProcessorSetStreamFrameFormat(c->vctx, c->vp, 0, D3D11_VIDEO_FRAME_FORMAT_PROGRESSIVE);
		ID3D11VideoContext_VideoProcessorSetStreamAutoProcessingMode(c->vctx, c->vp, 0, FALSE);
		RECT sr = {0, 0, c->dw, c->dh}, dr = {0, 0, c->ow, c->oh};
		ID3D11VideoContext_VideoProcessorSetStreamSourceRect(c->vctx, c->vp, 0, TRUE, &sr);
		ID3D11VideoContext_VideoProcessorSetStreamDestRect(c->vctx, c->vp, 0, TRUE, &dr);
		ID3D11VideoContext_VideoProcessorSetOutputTargetRect(c->vctx, c->vp, TRUE, &dr);
	}

	CHK(MFCreateDXGIDeviceManager(&c->token, &c->mgr), "MFCreateDXGIDeviceManager");
	CHK(IMFDXGIDeviceManager_ResetDevice(c->mgr, (IUnknown *)c->dev, c->token), "ResetDevice");

	for (int i = 0; i < c->nenc; i++) {
		c->enc[i].kbps = enc_kbps(c, i);
		if (open_encoder(c, &c->enc[i], allow_hw, err, errlen) != 0) goto fail_noset;
	}
	snprintf(c->name, sizeof c->name, "%s", c->enc[0].name);

	QueryPerformanceFrequency(&c->qpf);
	QueryPerformanceCounter(&c->t0);
	REL(out1); REL(out); REL(adp); REL(xdev);
	*outp = c;
	return 0;
fail:
fail_noset:
	REL(out1); REL(out); REL(adp); REL(xdev);
	rc_close(c);
	return RC_ERR;
}

static void append_out(rc_enc *e, const BYTE *p, DWORD n) {
	if (e->out_len + (int)n > e->out_cap) {
		e->out_cap = (e->out_len + n) * 2;
		e->out = realloc(e->out, e->out_cap);
	}
	memcpy(e->out + e->out_len, p, n);
	e->out_len += n;
}

// Puxa 1 saída do encoder. Retorna S_OK se obteve dados, MF_E_TRANSFORM_NEED_MORE_INPUT se não há.
static HRESULT pull_output(rc_enc *e) {
	HRESULT hr;
	for (;;) {
		MFT_OUTPUT_DATA_BUFFER ob;
		DWORD status = 0;
		memset(&ob, 0, sizeof ob);
		if (!e->provides) {
			if (!e->outsample) {
				IMFMediaBuffer *b = NULL;
				if (FAILED(hr = MFCreateMemoryBuffer(e->out_size, &b))) return hr;
				MFCreateSample(&e->outsample);
				IMFSample_AddBuffer(e->outsample, b);
				REL(b);
			}
			IMFSample_DeleteAllItems(e->outsample); // limpa CleanPoint do quadro anterior
			IMFMediaBuffer *b = NULL;
			IMFSample_GetBufferByIndex(e->outsample, 0, &b);
			IMFMediaBuffer_SetCurrentLength(b, 0);
			REL(b);
			ob.pSample = e->outsample;
		}
		hr = IMFTransform_ProcessOutput(e->mft, 0, 1, &ob, &status);
		REL(ob.pEvents);
		if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
			IMFMediaType *t = NULL;
			if (SUCCEEDED(IMFTransform_GetOutputAvailableType(e->mft, 0, 0, &t))) {
				IMFTransform_SetOutputType(e->mft, 0, t, 0);
				REL(t);
			}
			if (e->provides) REL(ob.pSample);
			continue;
		}
		if (FAILED(hr)) {
			if (e->provides) REL(ob.pSample);
			return hr;
		}
		IMFMediaBuffer *buf = NULL;
		if (ob.pSample && SUCCEEDED(IMFSample_ConvertToContiguousBuffer(ob.pSample, &buf))) {
			BYTE *p; DWORD n = 0;
			if (SUCCEEDED(IMFMediaBuffer_Lock(buf, &p, NULL, &n))) {
				append_out(e, p, n);
				IMFMediaBuffer_Unlock(buf);
			}
			REL(buf);
			UINT32 clean = 0;
			if (SUCCEEDED(IMFSample_GetUINT32(ob.pSample, &MFSampleExtension_CleanPoint, &clean)) && clean) e->out_key = 1;
		}
		if (e->provides) REL(ob.pSample);
		return S_OK;
	}
}

// Processa eventos pendentes do MFT assíncrono (hardware). Retorna 1 se saiu algum dado.
static int pump_async(rc_ctx *c, rc_enc *e, int wait_ms) {
	int got = 0;
	LARGE_INTEGER s, n;
	QueryPerformanceCounter(&s);
	for (;;) {
		IMFMediaEvent *ev = NULL;
		HRESULT hr = IMFMediaEventGenerator_GetEvent(e->events, MF_EVENT_FLAG_NO_WAIT, &ev);
		if (hr == MF_E_NO_EVENTS_AVAILABLE) {
			QueryPerformanceCounter(&n);
			int el = (int)((n.QuadPart - s.QuadPart) * 1000 / c->qpf.QuadPart);
			if (got || el >= wait_ms) return got;
			rc_nap(c);
			continue;
		}
		if (FAILED(hr)) return got ? got : -1;
		MediaEventType t = 0;
		IMFMediaEvent_GetType(ev, &t);
		REL(ev);
		if (t == METransformNeedInput) e->need_input++;
		else if (t == METransformHaveOutput) { if (SUCCEEDED(pull_output(e))) got = 1; }
	}
}

// submit entrega a textura NV12 tex ao encoder e, se ele for síncrono, já
// tira a saída. O assíncrono entrega depois, em collect.
static int submit(rc_ctx *c, rc_enc *e, ID3D11Texture2D *tex, int force_key) {
	HRESULT hr;
	IMFMediaBuffer *buf = NULL;
	IMFSample *s = NULL;
	int ret = RC_ERR;

	// o 1º quadro de um encoder novo já é IDR; evita loop de reabertura em encoders sem ForceKeyFrame
	if (force_key && e->api && e->nenc > 0) {
		VARIANT v; VariantInit(&v); v.vt = VT_UI4; v.ulVal = 1;
		if (FAILED(ICodecAPI_SetValue(e->api, &rcForceKey, &v))) return RC_REOPEN; // recria encoder => keyframe
	}

	if (e->d3d_in) {
		if (FAILED(MFCreateDXGISurfaceBuffer(&IID_ID3D11Texture2D, (IUnknown *)tex, 0, FALSE, &buf))) goto done;
		IMF2DBuffer *b2 = NULL;
		DWORD len = 0;
		if (SUCCEEDED(IMFMediaBuffer_QueryInterface(buf, &IID_IMF2DBuffer, (void **)&b2))) {
			IMF2DBuffer_GetContiguousLength(b2, &len);
			REL(b2);
		}
		IMFMediaBuffer_SetCurrentLength(buf, len);
	} else {
		// encoder software: copia NV12 da GPU para a memória
		D3D11_MAPPED_SUBRESOURCE m;
		ID3D11DeviceContext_CopyResource(c->ctx, (ID3D11Resource *)c->staging, (ID3D11Resource *)tex);
		if (FAILED(ID3D11DeviceContext_Map(c->ctx, (ID3D11Resource *)c->staging, 0, D3D11_MAP_READ, 0, &m))) goto done;
		DWORD sz = c->ow * c->oh * 3 / 2;
		BYTE *dst;
		if (FAILED(MFCreateMemoryBuffer(sz, &buf)) || FAILED(IMFMediaBuffer_Lock(buf, &dst, NULL, NULL))) {
			ID3D11DeviceContext_Unmap(c->ctx, (ID3D11Resource *)c->staging, 0);
			goto done;
		}
		const BYTE *src = m.pData;
		for (int y = 0; y < c->oh; y++) memcpy(dst + y * c->ow, src + y * m.RowPitch, c->ow);
		src += (size_t)m.RowPitch * c->oh;
		dst += c->ow * c->oh;
		for (int y = 0; y < c->oh / 2; y++) memcpy(dst + y * c->ow, src + y * m.RowPitch, c->ow);
		IMFMediaBuffer_Unlock(buf);
		ID3D11DeviceContext_Unmap(c->ctx, (ID3D11Resource *)c->staging, 0);
		IMFMediaBuffer_SetCurrentLength(buf, sz);
	}
	if (FAILED(MFCreateSample(&s))) goto done;
	IMFSample_AddBuffer(s, buf);
	LARGE_INTEGER now;
	QueryPerformanceCounter(&now);
	IMFSample_SetSampleTime(s, (LONGLONG)((now.QuadPart - c->t0.QuadPart) * 10000000.0 / c->qpf.QuadPart));
	IMFSample_SetSampleDuration(s, 10000000LL / c->fps);

	e->nenc++;
	if (e->async_) {
		// espera o encoder pedir entrada (máx ~500ms)
		for (int i = 0; e->need_input == 0 && i < 500; i++)
			if (pump_async(c, e, 1) < 0) goto done;
		if (e->need_input == 0) goto done;
		e->need_input--;
		if (FAILED(IMFTransform_ProcessInput(e->mft, 0, s, 0))) goto done;
	} else {
		hr = IMFTransform_ProcessInput(e->mft, 0, s, 0);
		if (hr == MF_E_NOTACCEPTING) {
			while (pull_output(e) == S_OK) {}
			hr = IMFTransform_ProcessInput(e->mft, 0, s, 0);
		}
		if (FAILED(hr)) goto done;
		while (pull_output(e) == S_OK) {}
	}
	ret = RC_NOTHING;
done:
	REL(s);
	REL(buf);
	return ret;
}

// encode_slot manda o slot a todos os encoders e espera as saídas. No
// AVC444 as duas visões vão para a GPU antes de esperar qualquer uma, e a
// espera é uma só para os dois encoders (com prazo comum): eles trabalham ao
// mesmo tempo, e esperar um depois do outro dobraria o tempo por quadro.
static int encode_slot(rc_ctx *c, int slot, int force_key) {
	for (int i = 0; i < c->nenc; i++) {
		int r = submit(c, &c->enc[i], c->nv12[i][slot], force_key);
		if (r < 0) return r;
	}
	LARGE_INTEGER s, n;
	QueryPerformanceCounter(&s);
	int wait_ms = 2000 / c->fps + 5;
	for (;;) {
		int pending = 0;
		for (int i = 0; i < c->nenc; i++) {
			rc_enc *e = &c->enc[i];
			if (e->async_ && e->out_len == 0 && pump_async(c, e, 0) <= 0) pending = 1;
		}
		if (!pending) break;
		QueryPerformanceCounter(&n);
		if ((n.QuadPart - s.QuadPart) * 1000 / c->qpf.QuadPart >= wait_ms) break;
		rc_nap(c);
	}
	return RC_NOTHING;
}

// draw_cursor removido: cursor é enviado como overlay CSS no browser (sem rastro)

// Retorna posição atual do cursor em coordenadas da tela capturada (relativas ao monitor).
// vis=1 se o cursor está visível. Usado pelo lado Go para enviar {"t":"cur","x":...,"y":...}.
static void rc_cursor_pos(rc_ctx *c, int *x, int *y, int *vis) {
	CURSORINFO ci; ci.cbSize = sizeof ci;
	if (GetCursorInfo(&ci)) {
		*x   = (int)((int64_t)(ci.ptScreenPos.x - c->left) * c->ow / c->dw);
		*y   = (int)((int64_t)(ci.ptScreenPos.y - c->top) * c->oh / c->dh);
		*vis = (ci.flags & CURSOR_SHOWING) ? 1 : 0;
	} else {
		*x = 0; *y = 0; *vis = 0;
	}
}

// Espera até timeout_ms por mudança na tela e codifica.
// flags: RC_FORCE_KEY (keyframe), RC_REPEAT (re-codifica o último quadro se a tela não mudou; melhora a nitidez parada).
// Retorna RC_OK_FRAME (pelo menos um encoder tem saída: leia com rc_output),
// RC_NOTHING, RC_REOPEN (desktop trocou: UAC/bloqueio/resolução) ou RC_ERR.
int rc_frame(rc_ctx *c, int timeout_ms, int flags) {
	DXGI_OUTDUPL_FRAME_INFO fi;
	IDXGIResource *res = NULL;
	ID3D11Texture2D *tex = NULL;
	int slot, r;
	for (int i = 0; i < c->nenc; i++) { c->enc[i].out_len = 0; c->enc[i].out_key = 0; }

	HRESULT hr = IDXGIOutputDuplication_AcquireNextFrame(c->dup, timeout_ms, &fi, &res);
	if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
		if (!(flags & (RC_FORCE_KEY | RC_REPEAT)) || !c->have_last) {
			int got = 0;
			for (int i = 0; i < c->nenc; i++) // entrega saídas atrasadas do encoder
				if (c->enc[i].async_ && pump_async(c, &c->enc[i], 0) > 0) got = 1;
			return got ? RC_OK_FRAME : RC_NOTHING;
		}
		slot = c->last;
	} else if (hr == DXGI_ERROR_ACCESS_LOST || hr == DXGI_ERROR_INVALID_CALL) {
		return RC_REOPEN;
	} else if (FAILED(hr)) {
		return RC_ERR;
	} else {
		if (SUCCEEDED(IDXGIResource_QueryInterface(res, &IID_ID3D11Texture2D, (void **)&tex))) {
			ID3D11DeviceContext_CopyResource(c->ctx, (ID3D11Resource*)c->bgra, (ID3D11Resource*)tex);
			REL(tex);
		}
		REL(res);
		IDXGIOutputDuplication_ReleaseFrame(c->dup);
		slot = c->ring;
		c->ring = (c->ring + 1) % c->ringn;
		if (c->mode == RC_AVC444) {
			draw_avc444(c, slot);
		} else {
			D3D11_VIDEO_PROCESSOR_STREAM st;
			memset(&st, 0, sizeof st);
			st.Enable = TRUE;
			st.pInputSurface = c->vin;
			if (FAILED(ID3D11VideoContext_VideoProcessorBlt(c->vctx, c->vp, c->vout[slot], 0, 1, &st))) return RC_ERR;
		}
		c->last = slot;
		c->have_last = 1;
	}
	r = encode_slot(c, slot, flags & RC_FORCE_KEY);
	if (r < 0) return r;
	for (int i = 0; i < c->nenc; i++)
		if (c->enc[i].out_len > 0) return RC_OK_FRAME;
	return RC_NOTHING;
}

// rc_output devolve a saída do encoder i (0 = principal, 1 = auxiliar no
// AVC444) do último rc_frame; *len = 0 se ele não entregou nada.
void rc_output(rc_ctx *c, int i, uint8_t **data, int *len, int *key) {
	rc_enc *e = &c->enc[i];
	*data = e->out;
	*len = e->out_len;
	*key = e->out_key;
}

// rc_seqhdr devolve o MF_MT_MPEG_SEQUENCE_HEADER do encoder (AV1), ou len 0.
void rc_seqhdr(rc_ctx *c, uint8_t **data, int *len) {
	*data = c->enc[0].seqhdr;
	*len = c->enc[0].seqhdr_len;
}
