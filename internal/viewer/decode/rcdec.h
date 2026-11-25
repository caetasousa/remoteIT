// rcdec.h: decodificador H.264 do visualizador nativo (Windows).
//
// Usa o decodificador H.264 do próprio Windows (Media Foundation, MFT
// síncrono). Com uma placa de vídeo compatível, liga o DXVA (decodificação
// na GPU) passando um dispositivo D3D11; sem ela, o mesmo MFT decodifica na
// CPU. A saída NV12 é convertida para BGRA (BT.709, faixa 16-235, a mesma do
// encoder do agente em internal/agent/media/h264/rcenc.h) para a janela
// desenhar com GDI.
#define COBJMACROS
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <d3d11.h>
#include <d3d10.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mftransform.h>
#include <mferror.h>
#include <codecapi.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef HRESULT (WINAPI *pfnMFStartup)(ULONG, DWORD);
typedef HRESULT (WINAPI *pfnMFCreateMediaType)(IMFMediaType **);
typedef HRESULT (WINAPI *pfnMFCreateSample)(IMFSample **);
typedef HRESULT (WINAPI *pfnMFCreateMemoryBuffer)(DWORD, IMFMediaBuffer **);
typedef HRESULT (WINAPI *pfnMFCreateDXGIDeviceManager)(UINT *, IMFDXGIDeviceManager **);
typedef HRESULT (WINAPI *pfnMFTEnumEx)(GUID, UINT32, const MFT_REGISTER_TYPE_INFO *, const MFT_REGISTER_TYPE_INFO *, IMFActivate ***, UINT32 *);
static pfnMFStartup dMFStartup;
static pfnMFCreateMediaType dMFCreateMediaType;
static pfnMFCreateSample dMFCreateSample;
static pfnMFCreateMemoryBuffer dMFCreateMemoryBuffer;
static pfnMFCreateDXGIDeviceManager dMFCreateDXGIDeviceManager;
static pfnMFTEnumEx dMFTEnumEx;

static int rd_load(void) {
	static int state = 0;
	if (state) return state;
	HMODULE m = LoadLibraryW(L"mfplat.dll");
	state = -1;
	if (!m) return state;
#define LOADF(n) if (!(d##n = (pfn##n)(void *)GetProcAddress(m, #n))) return state;
	LOADF(MFStartup) LOADF(MFCreateMediaType) LOADF(MFCreateSample) LOADF(MFCreateMemoryBuffer)
	LOADF(MFCreateDXGIDeviceManager) LOADF(MFTEnumEx)
#undef LOADF
	return state = 1;
}

#define RD_FRAME 1
#define RD_NONE  0
#define RD_ERR  -1

typedef struct {
	IMFTransform *mft;
	ID3D11Device *dev;
	IMFDXGIDeviceManager *mgr;
	int hw;          // 1 = DXVA (GPU)
	int provides;    // o MFT entrega as próprias amostras (modo GPU)
	DWORD out_size;  // tamanho do buffer de saída quando somos nós que alocamos
	UINT32 w, h;     // tamanho NV12 do decodificador (pode ter altura alinhada, ex. 1088)
	LONG stride;
	IMFSample *out_sample; // reaproveitada no modo CPU
	uint8_t *bgra;
	int bgra_cap;
	LONGLONG ts;
} rd_ctx;

static void rd_err(char *err, int n, const char *what, HRESULT hr) {
	snprintf(err, n, "%s: 0x%08lx", what, (unsigned long)hr);
}

static HRESULT rd_set_output(rd_ctx *c) {
	IMFMediaType *t = NULL;
	HRESULT hr;
	for (DWORD i = 0;; i++) {
		hr = IMFTransform_GetOutputAvailableType(c->mft, 0, i, &t);
		if (FAILED(hr)) return hr;
		GUID sub;
		if (SUCCEEDED(IMFMediaType_GetGUID(t, &MF_MT_SUBTYPE, &sub)) && IsEqualGUID(&sub, &MFVideoFormat_NV12)) break;
		IMFMediaType_Release(t);
		t = NULL;
	}
	hr = IMFTransform_SetOutputType(c->mft, 0, t, 0);
	if (SUCCEEDED(hr)) {
		UINT64 fs = 0;
		IMFMediaType_GetUINT64(t, &MF_MT_FRAME_SIZE, &fs);
		c->w = (UINT32)(fs >> 32);
		c->h = (UINT32)fs;
		UINT32 st = 0;
		c->stride = SUCCEEDED(IMFMediaType_GetUINT32(t, &MF_MT_DEFAULT_STRIDE, &st)) ? (LONG)st : (LONG)c->w;
		MFT_OUTPUT_STREAM_INFO info;
		if (SUCCEEDED(IMFTransform_GetOutputStreamInfo(c->mft, 0, &info))) {
			c->provides = (info.dwFlags & (MFT_OUTPUT_STREAM_PROVIDES_SAMPLES | MFT_OUTPUT_STREAM_CAN_PROVIDE_SAMPLES)) != 0;
			c->out_size = info.cbSize;
		}
		if (c->out_sample) {
			IMFSample_Release(c->out_sample);
			c->out_sample = NULL;
		}
	}
	IMFMediaType_Release(t);
	return hr;
}

// rd_try_hw liga o DXVA: dispositivo D3D11 com suporte a vídeo, protegido
// para várias threads, entregue ao MFT. Qualquer falha deixa o MFT na CPU.
static void rd_try_hw(rd_ctx *c) {
	IMFAttributes *a = NULL;
	UINT32 aware = 0;
	if (FAILED(IMFTransform_GetAttributes(c->mft, &a)) || !a) return;
	IMFAttributes_GetUINT32(a, &MF_SA_D3D11_AWARE, &aware);
	IMFAttributes_Release(a);
	if (!aware) return;
	D3D_FEATURE_LEVEL fl[] = {D3D_FEATURE_LEVEL_11_1, D3D_FEATURE_LEVEL_11_0, D3D_FEATURE_LEVEL_10_1};
	if (FAILED(D3D11CreateDevice(NULL, D3D_DRIVER_TYPE_HARDWARE, NULL, D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
			fl, 3, D3D11_SDK_VERSION, &c->dev, NULL, NULL))) return;
	ID3D10Multithread *mt = NULL;
	if (SUCCEEDED(ID3D11Device_QueryInterface(c->dev, &IID_ID3D10Multithread, (void **)&mt))) {
		ID3D10Multithread_SetMultithreadProtected(mt, TRUE);
		ID3D10Multithread_Release(mt);
	}
	UINT token = 0;
	if (FAILED(dMFCreateDXGIDeviceManager(&token, &c->mgr)) ||
		FAILED(IMFDXGIDeviceManager_ResetDevice(c->mgr, (IUnknown *)c->dev, token)) ||
		FAILED(IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_SET_D3D_MANAGER, (ULONG_PTR)c->mgr))) {
		IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_SET_D3D_MANAGER, 0);
		if (c->mgr) IMFDXGIDeviceManager_Release(c->mgr);
		ID3D11Device_Release(c->dev);
		c->mgr = NULL;
		c->dev = NULL;
		return;
	}
	c->hw = 1;
}

void rd_close(rd_ctx *c) {
	if (!c) return;
	if (c->out_sample) IMFSample_Release(c->out_sample);
	if (c->mft) {
		IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_NOTIFY_END_STREAMING, 0);
		IMFTransform_Release(c->mft);
	}
	if (c->mgr) IMFDXGIDeviceManager_Release(c->mgr);
	if (c->dev) ID3D11Device_Release(c->dev);
	free(c->bgra);
	free(c);
}

int rd_open(rd_ctx **out, int allow_hw, char *err, int errlen) {
	*out = NULL;
	if (rd_load() != 1) {
		snprintf(err, errlen, "Media Foundation indisponível");
		return RD_ERR;
	}
	HRESULT hr = CoInitializeEx(NULL, COINIT_MULTITHREADED);
	if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) { rd_err(err, errlen, "CoInitializeEx", hr); return RD_ERR; }
	if (FAILED(hr = dMFStartup(MF_VERSION, MFSTARTUP_LITE))) { rd_err(err, errlen, "MFStartup", hr); return RD_ERR; }

	rd_ctx *c = calloc(1, sizeof(rd_ctx));
	MFT_REGISTER_TYPE_INFO in = {MFMediaType_Video, MFVideoFormat_H264};
	IMFActivate **acts = NULL;
	UINT32 n = 0;
	hr = dMFTEnumEx(MFT_CATEGORY_VIDEO_DECODER, MFT_ENUM_FLAG_SYNCMFT | MFT_ENUM_FLAG_SORTANDFILTER, &in, NULL, &acts, &n);
	if (FAILED(hr) || n == 0) { rd_err(err, errlen, "MFTEnumEx (sem decodificador H.264)", hr); free(c); return RD_ERR; }
	hr = IMFActivate_ActivateObject(acts[0], &IID_IMFTransform, (void **)&c->mft);
	for (UINT32 i = 0; i < n; i++) IMFActivate_Release(acts[i]);
	CoTaskMemFree(acts);
	if (FAILED(hr)) { rd_err(err, errlen, "ActivateObject", hr); free(c); return RD_ERR; }

	IMFAttributes *a = NULL;
	if (SUCCEEDED(IMFTransform_GetAttributes(c->mft, &a)) && a) {
		IMFAttributes_SetUINT32(a, &MF_LOW_LATENCY, TRUE);
		IMFAttributes_Release(a);
	}
	if (allow_hw) rd_try_hw(c);

	IMFMediaType *t = NULL;
	dMFCreateMediaType(&t);
	IMFMediaType_SetGUID(t, &MF_MT_MAJOR_TYPE, &MFMediaType_Video);
	IMFMediaType_SetGUID(t, &MF_MT_SUBTYPE, &MFVideoFormat_H264);
	IMFMediaType_SetUINT32(t, &MF_MT_INTERLACE_MODE, MFVideoInterlace_Progressive);
	hr = IMFTransform_SetInputType(c->mft, 0, t, 0);
	IMFMediaType_Release(t);
	if (FAILED(hr)) { rd_err(err, errlen, "SetInputType", hr); rd_close(c); return RD_ERR; }
	if (FAILED(hr = rd_set_output(c))) { rd_err(err, errlen, "SetOutputType NV12", hr); rd_close(c); return RD_ERR; }
	IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_NOTIFY_BEGIN_STREAMING, 0);
	IMFTransform_ProcessMessage(c->mft, MFT_MESSAGE_NOTIFY_START_OF_STREAM, 0);
	*out = c;
	return 0;
}

int rd_is_hw(rd_ctx *c) { return c->hw; }

static inline uint8_t clamp8(int v) { return v < 0 ? 0 : v > 255 ? 255 : (uint8_t)v; }

// nv12_to_bgra recorta w x h do quadro (o decodificador pode alinhar a altura)
// e converte com BT.709 faixa limitada, em ponto fixo (x1024).
static void nv12_to_bgra(const uint8_t *src, LONG pitch, UINT32 plane_h, int w, int h, uint8_t *dst) {
	const uint8_t *uv = src + (size_t)pitch * plane_h;
	for (int y = 0; y < h; y++) {
		const uint8_t *yr = src + (size_t)pitch * y;
		const uint8_t *cr = uv + (size_t)pitch * (y / 2);
		uint8_t *o = dst + (size_t)w * 4 * y;
		for (int x = 0; x < w; x++) {
			int Y = 1192 * (yr[x] - 16);
			int U = cr[x & ~1] - 128, V = cr[(x & ~1) + 1] - 128;
			o[0] = clamp8((Y + 2163 * U) >> 10);
			o[1] = clamp8((Y - 218 * U - 546 * V) >> 10);
			o[2] = clamp8((Y + 1836 * V) >> 10);
			o[3] = 255;
			o += 4;
		}
	}
}

static int rd_copy_out(rd_ctx *c, IMFSample *s, int w, int h) {
	IMFMediaBuffer *b = NULL;
	if (FAILED(IMFSample_GetBufferByIndex(s, 0, &b))) return RD_NONE;
	if (w <= 0 || w > (int)c->w) w = (int)c->w;
	if (h <= 0 || h > (int)c->h) h = (int)c->h;
	int need = w * h * 4;
	if (need > c->bgra_cap) {
		free(c->bgra);
		c->bgra = malloc(need);
		c->bgra_cap = c->bgra ? need : 0;
		if (!c->bgra) { IMFMediaBuffer_Release(b); return RD_ERR; }
	}
	int ok = RD_NONE;
	IMF2DBuffer *b2 = NULL;
	BYTE *p = NULL;
	LONG pitch = 0;
	// Lock2D devolve o pitch real; num buffer da GPU, o Media Foundation copia
	// a superfície para a memória ao travar
	if (SUCCEEDED(IMFMediaBuffer_QueryInterface(b, &IID_IMF2DBuffer, (void **)&b2)) &&
		SUCCEEDED(IMF2DBuffer_Lock2D(b2, &p, &pitch))) {
		if (pitch > 0) { nv12_to_bgra(p, pitch, c->h, w, h, c->bgra); ok = RD_FRAME; }
		IMF2DBuffer_Unlock2D(b2);
	} else {
		DWORD len = 0;
		if (SUCCEEDED(IMFMediaBuffer_Lock(b, &p, NULL, &len))) {
			LONG st = c->stride > 0 ? c->stride : (LONG)c->w;
			if (len >= (DWORD)(st * c->h * 3 / 2)) { nv12_to_bgra(p, st, c->h, w, h, c->bgra); ok = RD_FRAME; }
			IMFMediaBuffer_Unlock(b);
		}
	}
	if (b2) IMF2DBuffer_Release(b2);
	IMFMediaBuffer_Release(b);
	return ok;
}

// rd_drain tira os quadros prontos; devolve RD_FRAME se pelo menos um saiu.
static int rd_drain(rd_ctx *c, int w, int h) {
	int got = RD_NONE;
	for (;;) {
		MFT_OUTPUT_DATA_BUFFER ob = {0};
		DWORD status = 0;
		if (!c->provides) {
			if (!c->out_sample) {
				IMFMediaBuffer *mb = NULL;
				DWORD size = c->out_size ? c->out_size : c->w * c->h * 3 / 2;
				if (FAILED(dMFCreateSample(&c->out_sample)) || FAILED(dMFCreateMemoryBuffer(size, &mb))) return RD_ERR;
				IMFSample_AddBuffer(c->out_sample, mb);
				IMFMediaBuffer_Release(mb);
			}
			ob.pSample = c->out_sample;
		}
		HRESULT hr = IMFTransform_ProcessOutput(c->mft, 0, 1, &ob, &status);
		if (ob.pEvents) IMFCollection_Release(ob.pEvents);
		if (hr == MF_E_TRANSFORM_NEED_MORE_INPUT) return got;
		if (hr == MF_E_TRANSFORM_STREAM_CHANGE) {
			if (FAILED(rd_set_output(c))) return RD_ERR;
			continue;
		}
		if (FAILED(hr)) return got ? got : RD_ERR;
		int r = rd_copy_out(c, ob.pSample, w, h);
		if (c->provides && ob.pSample) IMFSample_Release(ob.pSample);
		if (r == RD_ERR) return RD_ERR;
		if (r == RD_FRAME) got = RD_FRAME;
	}
}

// rd_decode entrega um quadro Annex-B e, se sair um quadro decodificado,
// devolve RD_FRAME com *out apontando para w*h*4 bytes BGRA (válidos até a
// próxima chamada). w e h são os do cabeçalho do quadro (tamanho visível).
int rd_decode(rd_ctx *c, const uint8_t *data, int n, int w, int h, uint8_t **out, int *ow, int *oh) {
	IMFSample *s = NULL;
	IMFMediaBuffer *mb = NULL;
	BYTE *p = NULL;
	if (FAILED(dMFCreateSample(&s))) return RD_ERR;
	if (FAILED(dMFCreateMemoryBuffer((DWORD)n, &mb))) { IMFSample_Release(s); return RD_ERR; }
	IMFMediaBuffer_Lock(mb, &p, NULL, NULL);
	memcpy(p, data, n);
	IMFMediaBuffer_Unlock(mb);
	IMFMediaBuffer_SetCurrentLength(mb, (DWORD)n);
	IMFSample_AddBuffer(s, mb);
	IMFMediaBuffer_Release(mb);
	IMFSample_SetSampleTime(s, c->ts);
	IMFSample_SetSampleDuration(s, 166666);
	c->ts += 166666;

	int got = RD_NONE;
	HRESULT hr = IMFTransform_ProcessInput(c->mft, 0, s, 0);
	if (hr == MF_E_NOTACCEPTING) { // há quadros prontos esperando: tira e tenta de novo
		got = rd_drain(c, w, h);
		hr = IMFTransform_ProcessInput(c->mft, 0, s, 0);
	}
	IMFSample_Release(s);
	if (FAILED(hr)) return RD_ERR;
	int r = rd_drain(c, w, h);
	if (r == RD_ERR) return RD_ERR;
	if (r == RD_FRAME) got = RD_FRAME;
	if (got == RD_FRAME) {
		*out = c->bgra;
		*ow = (w > 0 && w <= (int)c->w) ? w : (int)c->w;
		*oh = (h > 0 && h <= (int)c->h) ? h : (int)c->h;
	}
	return got;
}
