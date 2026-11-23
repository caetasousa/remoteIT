// rcenc.h - captura + codificação H.264 na GPU para Windows (incluído só por h264_windows.go).
//
// Pipeline (tudo na GPU quando há encoder de hardware):
//   DXGI Desktop Duplication (só entrega quadro quando a tela muda)
//     -> textura BGRA (+ cursor desenhado via GDI)
//     -> D3D11 Video Processor (escala + BGRA->NV12 BT.709)
//     -> Media Foundation H.264 MFT (NVENC / Intel QSV / AMD AMF; fallback: encoder software da Microsoft)
//     -> Annex-B H.264, decodificado no navegador via WebCodecs.
#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <d3d10.h>
#include <dxgi1_2.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mftransform.h>
#include <mferror.h>
#include <codecapi.h>
#include <wmcodecdsp.h>
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

#define RC_FORCE_KEY 1
#define RC_REPEAT    2
#define RC_RING      8

#define RC_OK_FRAME   1
#define RC_NOTHING    0
#define RC_ERR       -1
#define RC_REOPEN    -2

static const GUID rcLowLatency = {STATIC_CODECAPI_AVLowLatencyMode};
static const GUID rcRateCtl    = {STATIC_CODECAPI_AVEncCommonRateControlMode};
static const GUID rcMeanBR     = {STATIC_CODECAPI_AVEncCommonMeanBitRate};
static const GUID rcGOP        = {STATIC_CODECAPI_AVEncMPVGOPSize};
static const GUID rcBFrames    = {STATIC_CODECAPI_AVEncMPVDefaultBPictureCount};
static const GUID rcQvS        = {STATIC_CODECAPI_AVEncCommonQualityVsSpeed};
static const GUID rcForceKey   = {STATIC_CODECAPI_AVEncVideoForceKeyFrame};
// intra-refresh: reescreve a tela em faixas contínuas => limpa fantasmas sem pico de banda
static const GUID rcIRMode  = {0xb998208, 0xffc, 0x4a7f, {0x92, 0x5b, 0xdd, 0x4a, 0x74, 0x8b, 0xa, 0xfc}}; // AVEncVideoIntraRefreshMode
static const GUID rcIRSize  = {0xd4a6e21e, 0x58cb, 0x4a95, {0xb4, 0x5, 0x9b, 0x7f, 0x4a, 0xf6, 0xa6, 0x5c}}; // AVEncVideoIntraRefreshMaxFrameCount (approx)
static const GUID rcBufSize = {STATIC_CODECAPI_AVEncCommonBufferSize};
static const GUID rcMaxBR   = {STATIC_CODECAPI_AVEncCommonMaxBitRate};
// não exportado pelo mingw: {1d39518c-e220-4da8-a07f-ba172552d6b1}
static const GUID rcEnumLUID   = {0x1d39518c, 0xe220, 0x4da8, {0xa0, 0x7f, 0xba, 0x17, 0x25, 0x52, 0xd6, 0xb1}};

typedef struct rc_ctx {
	ID3D11Device *dev;
	ID3D11DeviceContext *ctx;
	ID3D11VideoDevice *vdev;
	ID3D11VideoContext *vctx;
	IDXGIOutputDuplication *dup;
	LUID luid;
	int left, top, dw, dh, ow, oh, fps, kbps;

	ID3D11Texture2D *bgra;  // tela capturada (sem cursor, entra no encoder)
	ID3D11VideoProcessorEnumerator *vpe;
	ID3D11VideoProcessor *vp;
	ID3D11VideoProcessorInputView *vin;
	ID3D11Texture2D *nv12[RC_RING];
	ID3D11VideoProcessorOutputView *vout[RC_RING];
	ID3D11Texture2D *staging;
	int ring, last, have_last;

	IMFActivate *act;
	IMFTransform *mft;
	ICodecAPI *api;
	IMFMediaEventGenerator *events;
	IMFDXGIDeviceManager *mgr;
	UINT token;
	int hw, async_, d3d_in, provides;
	DWORD out_size;
	int need_input;
	int nenc; // quadros já enviados ao encoder atual
	IMFSample *outsample;

	LARGE_INTEGER qpf, t0;
	uint8_t *out;
	int out_len, out_cap, out_key;
	char name[160];
	int com, mfs;
} rc_ctx;

#define REL(p) do { if (p) { (p)->lpVtbl->Release(p); (p) = NULL; } } while (0)
#define CHK(x, msg) do { hr = (x); if (FAILED(hr)) { snprintf(err, errlen, "%s: 0x%08lx", msg, (unsigned long)hr); goto fail; } } while (0)

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

static void close_encoder(rc_ctx *c) {
	if (c->mft) {
		IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
		IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_SET_D3D_MANAGER, 0);
	}
	REL(c->outsample);
	REL(c->events);
	REL(c->api);
	REL(c->mft);
	if (c->act) { IMFActivate_ShutdownObject(c->act); REL(c->act); }
	c->need_input = 0;
	c->nenc = 0;
}

// Configura um MFT já criado: tipos de mídia, baixa latência, sem B-frames, CBR.
static HRESULT configure_mft(rc_ctx *c, char *err, int errlen) {
	HRESULT hr;
	IMFAttributes *attrs = NULL;
	IMFMediaType *ot = NULL, *it = NULL;
	UINT32 v = 0;

	c->async_ = 0; c->d3d_in = 0;
	if (SUCCEEDED(IMFTransform_GetAttributes(c->mft, &attrs)) && attrs) {
		if (SUCCEEDED(IMFAttributes_GetUINT32(attrs, &MF_TRANSFORM_ASYNC, &v)) && v) {
			c->async_ = 1;
			IMFAttributes_SetUINT32(attrs, &MF_TRANSFORM_ASYNC_UNLOCK, TRUE);
		}
		v = 0;
		if (c->hw && SUCCEEDED(IMFAttributes_GetUINT32(attrs, &MF_SA_D3D11_AWARE, &v)) && v) c->d3d_in = 1;
		REL(attrs);
	}
	if (c->hw && !c->d3d_in) { snprintf(err, errlen, "MFT de hardware sem suporte D3D11"); return E_FAIL; }
	if (c->d3d_in)
		CHK(IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)c->mgr), "SET_D3D_MANAGER");

	IMFTransform_QueryInterface(c->mft, &IID_ICodecAPI, (void **)&c->api);
	capi_bool(c->api, &rcLowLatency, 1);
	capi_u4(c->api, &rcRateCtl, eAVEncCommonRateControlMode_CBR);
	capi_u4(c->api, &rcMeanBR, (ULONG)c->kbps * 1000);
	capi_u4(c->api, &rcGOP, (ULONG)c->fps);         // keyframe a cada 1s
	capi_u4(c->api, &rcIRMode, 0);                  // sem intra-refresh: com bitrate alto não precisa
	capi_u4(c->api, &rcBufSize, (ULONG)c->kbps * 125); // buffer VBV = 1 frame (~8ms) → latência mínima
	capi_u4(c->api, &rcMaxBR, (ULONG)c->kbps * 1500); // permite rajadas de até 50% nos keyframes
	capi_u4(c->api, &rcBFrames, 0);
	capi_u4(c->api, &rcQvS, 100); // 100 = prioriza velocidade (menor latência)

	// tipo de saída primeiro (regra de encoders MF); tenta High -> Main -> Baseline
	static const UINT32 profiles[] = {eAVEncH264VProfile_High, eAVEncH264VProfile_Main, eAVEncH264VProfile_Base};
	hr = E_FAIL;
	for (int i = 0; i < 3 && FAILED(hr); i++) {
		REL(ot);
		CHK(MFCreateMediaType(&ot), "MFCreateMediaType");
		IMFMediaType_SetGUID(ot, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
		IMFMediaType_SetGUID(ot, &MF_MT_SUBTYPE, &MFVideoFormat_H264);
		IMFMediaType_SetUINT32(ot, &MF_MT_AVG_BITRATE, (UINT32)c->kbps * 1000);
		set_size(ot, &MF_MT_FRAME_SIZE, c->ow, c->oh);
		set_size(ot, &MF_MT_FRAME_RATE, c->fps, 1);
		set_size(ot, &MF_MT_PIXEL_ASPECT_RATIO, 1, 1);
		IMFMediaType_SetUINT32(ot, &MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
		IMFMediaType_SetUINT32(ot, &MF_MT_MPEG2_PROFILE, profiles[i]);
		hr = IMFTransform_SetOutputType(c->mft, 0, ot, 0);
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
	IMFMediaType_SetUINT32(it, &MF_MT_VIDEO_NOMINAL_RANGE, MFNominalRange_16_235);
	CHK(IMFTransform_SetInputType(c->mft, 0, it, 0), "SetInputType");

	MFT_OUTPUT_STREAM_INFO si;
	memset(&si, 0, sizeof si);
	CHK(IMFTransform_GetOutputStreamInfo(c->mft, 0, &si), "GetOutputStreamInfo");
	c->provides = (si.dwFlags & MFT_OUTPUT_STREAM_PROVIDES_SAMPLES) != 0;
	c->out_size = si.cbSize ? si.cbSize : (DWORD)(c->ow * c->oh * 2);

	if (c->async_)
		CHK(IMFTransform_QueryInterface(c->mft, &IID_IMFMediaEventGenerator, (void **)&c->events), "EventGenerator");

	IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_COMMAND_FLUSH, 0);
	CHK(IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0), "BEGIN_STREAMING");
	CHK(IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0), "START_OF_STREAM");
	hr = S_OK;
fail:
	REL(ot);
	REL(it);
	return hr;
}

static int open_encoder(rc_ctx *c, int allow_hw, char *err, int errlen) {
	HRESULT hr;
	if (allow_hw) {
		MFT_REGISTER_TYPE_INFO in = {MFMediaType_Video, MFVideoFormat_NV12};
		MFT_REGISTER_TYPE_INFO out = {MFMediaType_Video, MFVideoFormat_H264};
		IMFActivate **acts = NULL;
		UINT32 n = 0;
		if (SUCCEEDED(MFTEnumEx(MFT_CATEGORY_VIDEO_ENCODER, MFT_ENUM_FLAG_HARDWARE | MFT_ENUM_FLAG_SORTANDFILTER,
		                        &in, &out, &acts, &n))) {
			for (UINT32 i = 0; i < n; i++) {
				IMFActivate *a = acts[i];
				acts[i] = NULL;
				if (c->mft) { REL(a); continue; }
				// em PCs com 2 GPUs, usa só o encoder da GPU que tem o desktop
				LUID l; UINT32 bl = 0; UINT64 l64 = 0;
				UINT64 mine = ((UINT64)(UINT32)c->luid.HighPart << 32) | c->luid.LowPart;
				if (SUCCEEDED(IMFActivate_GetBlob(a, &rcEnumLUID, (UINT8 *)&l, sizeof l, &bl)) && bl == sizeof l) {
					if (l.LowPart != c->luid.LowPart || l.HighPart != c->luid.HighPart) { REL(a); continue; }
				} else if (SUCCEEDED(IMFActivate_GetUINT64(a, &rcEnumLUID, &l64)) && l64 != mine) { REL(a); continue; }
				c->hw = 1;
				if (FAILED(IMFActivate_ActivateObject(a, &IID_IMFTransform, (void **)&c->mft))) { REL(a); continue; }
				c->act = a;
				WCHAR *wn = NULL; UINT32 wl = 0;
				if (SUCCEEDED(IMFActivate_GetAllocatedString(a, &MFT_FRIENDLY_NAME_Attribute, &wn, &wl))) {
					WideCharToMultiByte(CP_UTF8, 0, wn, -1, c->name, sizeof c->name, NULL, NULL);
					CoTaskMemFree(wn);
				}
				char e2[200];
				if (FAILED(configure_mft(c, e2, sizeof e2))) close_encoder(c);
			}
			CoTaskMemFree(acts);
		}
		if (c->mft) return 0;
	}
	c->hw = 0;
	snprintf(c->name, sizeof c->name, "Microsoft H.264 (software)");
	CHK(CoCreateInstance(&CLSID_CMSH264EncoderMFT, NULL, CLSCTX_INPROC_SERVER, &IID_IMFTransform, (void **)&c->mft),
	    "encoder software");
	CHK(configure_mft(c, err, errlen), "configurar encoder software");
	return 0;
fail:
	close_encoder(c);
	return -1;
}

void rc_close(rc_ctx *c) {
	if (!c) return;
	close_encoder(c);
	REL(c->staging);
	for (int i = 0; i < RC_RING; i++) { REL(c->vout[i]); REL(c->nv12[i]); }
	REL(c->vin);
	REL(c->vp);
	REL(c->vpe);
	// clean texture removed
	REL(c->bgra);
	REL(c->dup);
	REL(c->mgr);
	REL(c->vctx);
	REL(c->vdev);
	REL(c->ctx);
	REL(c->dev);
	if (c->mfs) MFShutdown();
	if (c->com) CoUninitialize();
	free(c->out);
	free(c);
}

const char *rc_name(rc_ctx *c) { return c->name; }
int rc_is_hw(rc_ctx *c) { return c->hw; }
int rc_width(rc_ctx *c) { return c->ow; }
int rc_height(rc_ctx *c) { return c->oh; }

void rc_set_bitrate(rc_ctx *c, int kbps) {
	c->kbps = kbps;
	capi_u4(c->api, &rcMeanBR, (ULONG)kbps * 1000);
}

int rc_open(rc_ctx **outp, int maxw, int fps, int kbps, int allow_hw, char *err, int errlen) {
	HRESULT hr;
	rc_ctx *c = calloc(1, sizeof *c);
	IDXGIDevice *xdev = NULL;
	IDXGIAdapter *adp = NULL;
	IDXGIOutput *out = NULL;
	IDXGIOutput1 *out1 = NULL;
	ID3D10Multithread *mt = NULL;
	*outp = NULL;
	c->fps = fps;
	c->kbps = kbps;

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

	td.Width = c->ow; td.Height = c->oh; td.Format = DXGI_FORMAT_NV12;
	td.BindFlags = D3D11_BIND_RENDER_TARGET | D3D11_BIND_VIDEO_ENCODER; td.MiscFlags = 0;
	D3D11_VIDEO_PROCESSOR_OUTPUT_VIEW_DESC ovd;
	memset(&ovd, 0, sizeof ovd);
	ovd.ViewDimension = D3D11_VPOV_DIMENSION_TEXTURE2D;
	for (int i = 0; i < RC_RING; i++) {
		hr = ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->nv12[i]);
		if (FAILED(hr) && (td.BindFlags & D3D11_BIND_VIDEO_ENCODER)) {
			td.BindFlags = D3D11_BIND_RENDER_TARGET; // runtime antigo
			hr = ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->nv12[i]);
		}
		CHK(hr, "textura NV12");
		CHK(ID3D11VideoDevice_CreateVideoProcessorOutputView(c->vdev, (ID3D11Resource *)c->nv12[i], c->vpe, &ovd, &c->vout[i]), "OutputView");
	}
	td.BindFlags = 0; td.Usage = D3D11_USAGE_STAGING; td.CPUAccessFlags = D3D11_CPU_ACCESS_READ;
	CHK(ID3D11Device_CreateTexture2D(c->dev, &td, NULL, &c->staging), "textura staging");

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

	CHK(MFCreateDXGIDeviceManager(&c->token, &c->mgr), "MFCreateDXGIDeviceManager");
	CHK(IMFDXGIDeviceManager_ResetDevice(c->mgr, (IUnknown *)c->dev, c->token), "ResetDevice");

	if (open_encoder(c, allow_hw, err, errlen) != 0) goto fail_noset;

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

static void append_out(rc_ctx *c, const BYTE *p, DWORD n) {
	if (c->out_len + (int)n > c->out_cap) {
		c->out_cap = (c->out_len + n) * 2;
		c->out = realloc(c->out, c->out_cap);
	}
	memcpy(c->out + c->out_len, p, n);
	c->out_len += n;
}

// Puxa 1 saída do encoder. Retorna S_OK se obteve dados, MF_E_TRANSFORM_NEED_MORE_INPUT se não há.
static HRESULT pull_output(rc_ctx *c) {
	HRESULT hr;
	for (;;) {
		MFT_OUTPUT_DATA_BUFFER ob;
		DWORD status = 0;
		memset(&ob, 0, sizeof ob);
		if (!c->provides) {
			if (!c->outsample) {
				IMFMediaBuffer *b = NULL;
				if (FAILED(hr = MFCreateMemoryBuffer(c->out_size, &b))) return hr;
				MFCreateSample(&c->outsample);
				IMFSample_AddBuffer(c->outsample, b);
				REL(b);
			}
			IMFSample_DeleteAllItems(c->outsample); // limpa CleanPoint do quadro anterior
			IMFMediaBuffer *b = NULL;
			IMFSample_GetBufferByIndex(c->outsample, 0, &b);
			IMFMediaBuffer_SetCurrentLength(b, 0);
			REL(b);
			ob.pSample = c->outsample;
		}
		hr = IMFTransform_ProcessOutput(c->mft, 0, 1, &ob, &status);
		REL(ob.pEvents);
		if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
			IMFMediaType *t = NULL;
			if (SUCCEEDED(IMFTransform_GetOutputAvailableType(c->mft, 0, 0, &t))) {
				IMFTransform_SetOutputType(c->mft, 0, t, 0);
				REL(t);
			}
			if (c->provides) REL(ob.pSample);
			continue;
		}
		if (FAILED(hr)) {
			if (c->provides) REL(ob.pSample);
			return hr;
		}
		IMFMediaBuffer *buf = NULL;
		if (ob.pSample && SUCCEEDED(IMFSample_ConvertToContiguousBuffer(ob.pSample, &buf))) {
			BYTE *p; DWORD n = 0;
			if (SUCCEEDED(IMFMediaBuffer_Lock(buf, &p, NULL, &n))) {
				append_out(c, p, n);
				IMFMediaBuffer_Unlock(buf);
			}
			REL(buf);
			UINT32 clean = 0;
			if (SUCCEEDED(IMFSample_GetUINT32(ob.pSample, &MFSampleExtension_CleanPoint, &clean)) && clean) c->out_key = 1;
		}
		if (c->provides) REL(ob.pSample);
		return S_OK;
	}
}

// Processa eventos pendentes do MFT assíncrono (hardware). Retorna 1 se saiu algum dado.
static int pump_async(rc_ctx *c, int wait_ms) {
	int got = 0;
	LARGE_INTEGER s, n;
	QueryPerformanceCounter(&s);
	for (;;) {
		IMFMediaEvent *ev = NULL;
		HRESULT hr = IMFMediaEventGenerator_GetEvent(c->events, MF_EVENT_FLAG_NO_WAIT, &ev);
		if (hr == MF_E_NO_EVENTS_AVAILABLE) {
			QueryPerformanceCounter(&n);
			int el = (int)((n.QuadPart - s.QuadPart) * 1000 / c->qpf.QuadPart);
			if (got || el >= wait_ms) return got;
			Sleep(1);
			continue;
		}
		if (FAILED(hr)) return got ? got : -1;
		MediaEventType t = 0;
		IMFMediaEvent_GetType(ev, &t);
		REL(ev);
		if (t == METransformNeedInput) c->need_input++;
		else if (t == METransformHaveOutput) { if (SUCCEEDED(pull_output(c))) got = 1; }
	}
}

static int encode_slot(rc_ctx *c, int slot, int force_key) {
	HRESULT hr;
	IMFMediaBuffer *buf = NULL;
	IMFSample *s = NULL;
	int ret = RC_ERR;

	// o 1º quadro de um encoder novo já é IDR; evita loop de reabertura em encoders sem ForceKeyFrame
	if (force_key && c->api && c->nenc > 0) {
		VARIANT v; VariantInit(&v); v.vt = VT_UI4; v.ulVal = 1;
		if (FAILED(ICodecAPI_SetValue(c->api, &rcForceKey, &v))) return RC_REOPEN; // recria encoder => keyframe
	}

	if (c->d3d_in) {
		if (FAILED(MFCreateDXGISurfaceBuffer(&IID_ID3D11Texture2D, (IUnknown *)c->nv12[slot], 0, FALSE, &buf))) goto done;
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
		ID3D11DeviceContext_CopyResource(c->ctx, (ID3D11Resource *)c->staging, (ID3D11Resource *)c->nv12[slot]);
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

	c->nenc++;
	if (c->async_) {
		// espera o encoder pedir entrada (máx ~500ms)
		for (int i = 0; c->need_input == 0 && i < 500; i++)
			if (pump_async(c, 1) < 0) goto done;
		if (c->need_input == 0) goto done;
		c->need_input--;
		if (FAILED(IMFTransform_ProcessInput(c->mft, 0, s, 0))) goto done;
		pump_async(c, 2000 / c->fps + 5);
	} else {
		hr = IMFTransform_ProcessInput(c->mft, 0, s, 0);
		if (hr == MF_E_NOTACCEPTING) {
			while (pull_output(c) == S_OK) {}
			hr = IMFTransform_ProcessInput(c->mft, 0, s, 0);
		}
		if (FAILED(hr)) goto done;
		while (pull_output(c) == S_OK) {}
	}
	ret = c->out_len > 0 ? RC_OK_FRAME : RC_NOTHING;
done:
	REL(s);
	REL(buf);
	return ret;
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
// Retorna RC_OK_FRAME (dados em *data/*len), RC_NOTHING, RC_REOPEN (desktop trocou: UAC/bloqueio/resolução) ou RC_ERR.
int rc_frame(rc_ctx *c, int timeout_ms, int flags, uint8_t **data, int *len, int *key) {
	DXGI_OUTDUPL_FRAME_INFO fi;
	IDXGIResource *res = NULL;
	ID3D11Texture2D *tex = NULL;
	int slot, r;
	c->out_len = 0;
	c->out_key = 0;
	*data = NULL; *len = 0; *key = 0;

	HRESULT hr = IDXGIOutputDuplication_AcquireNextFrame(c->dup, timeout_ms, &fi, &res);
	if (hr == DXGI_ERROR_WAIT_TIMEOUT) {
		if (!(flags & (RC_FORCE_KEY | RC_REPEAT)) || !c->have_last) {
			if (c->async_) { // entrega saídas atrasadas do encoder
				if (pump_async(c, 0) > 0) goto out;
			}
			return RC_NOTHING;
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
		c->ring = (c->ring + 1) % RC_RING;
		D3D11_VIDEO_PROCESSOR_STREAM st;
		memset(&st, 0, sizeof st);
		st.Enable = TRUE;
		st.pInputSurface = c->vin;
		if (FAILED(ID3D11VideoContext_VideoProcessorBlt(c->vctx, c->vp, c->vout[slot], 0, 1, &st))) return RC_ERR;
		c->last = slot;
		c->have_last = 1;
	}
	r = encode_slot(c, slot, flags & RC_FORCE_KEY);
	if (r < 0) return r;
out:
	if (c->out_len == 0) return RC_NOTHING;
	*data = c->out; *len = c->out_len; *key = c->out_key;
	return RC_OK_FRAME;
}
