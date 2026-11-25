// yuv.h: conversões de cor do visualizador, em C portátil (sem Windows).
//
// Fica separado de rcdec.h para os testes rodarem no Linux: yuv_cgo.go o
// compila em qualquer sistema e decode_test.go compara o resultado com a
// referência em Go (internal/avc444).
#ifndef RC_YUV_H
#define RC_YUV_H

#include <stdint.h>
#include <stddef.h>

static inline uint8_t yuv_clamp8(int v) { return v < 0 ? 0 : v > 255 ? 255 : (uint8_t)v; }

// nv12_to_bgra converte NV12 (BT.709 faixa limitada 16-235, a do H.264 e do
// AV1 comuns do agente) para BGRA, em ponto fixo (x1024). src tem o plano Y
// com pitch bytes por linha e o plano UV logo depois de plane_h linhas; só
// w x h é convertido (o decodificador pode alinhar a altura, ex. 1088).
static void nv12_to_bgra(const uint8_t *src, long pitch, unsigned plane_h, int w, int h, uint8_t *dst) {
	const uint8_t *uv = src + (size_t)pitch * plane_h;
	for (int y = 0; y < h; y++) {
		const uint8_t *yr = src + (size_t)pitch * y;
		const uint8_t *cr = uv + (size_t)pitch * (y / 2);
		uint8_t *o = dst + (size_t)w * 4 * y;
		for (int x = 0; x < w; x++) {
			int Y = 1192 * (yr[x] - 16);
			int U = cr[x & ~1] - 128, V = cr[(x & ~1) + 1] - 128;
			o[0] = yuv_clamp8((Y + 2163 * U) >> 10);
			o[1] = yuv_clamp8((Y - 218 * U - 546 * V) >> 10);
			o[2] = yuv_clamp8((Y + 1836 * V) >> 10);
			o[3] = 255;
			o += 4;
		}
	}
}

// Volta de BT.709 faixa completa em ponto fixo (x65536): os mesmos
// coeficientes de internal/avc444.ToRGB.
#define YUV_VR 103206
#define YUV_UG 12276
#define YUV_VG 30679
#define YUV_UB 121608

static inline void yuv_full_px(uint8_t *o, int y, int u, int v) {
	int Y = (y << 16) + (1 << 15);
	u -= 128;
	v -= 128;
	o[0] = yuv_clamp8((Y + YUV_UB * u) >> 16);
	o[1] = yuv_clamp8((Y - YUV_UG * u - YUV_VG * v) >> 16);
	o[2] = yuv_clamp8((Y + YUV_VR * v) >> 16);
	o[3] = 255;
}

// avc444_to_bgra recompõe a imagem 4:4:4 a partir das visões principal e
// auxiliar, ambas NV12 contíguas de w x h (veja internal/avc444 para o
// arranjo). aux NULL usa só a principal (cor repetida em blocos 2x2).
static void avc444_to_bgra(const uint8_t *mainp, const uint8_t *aux, int w, int h, uint8_t *dst) {
	const uint8_t *muv = mainp + (size_t)w * h;
	const uint8_t *auv = aux ? aux + (size_t)w * h : NULL;
	for (int y = 0; y < h; y++) {
		const uint8_t *yr = mainp + (size_t)w * y;
		uint8_t *o = dst + (size_t)w * 4 * y;
		const uint8_t *mc = muv + (size_t)w * (y / 2);
		if (!aux) {
			for (int x = 0; x < w; x++, o += 4) yuv_full_px(o, yr[x], mc[x & ~1], mc[(x & ~1) + 1]);
		} else if (y & 1) {
			// linha ímpar: U e V inteiros no plano Y auxiliar
			const uint8_t *ur = aux + (size_t)w * (y / 2);
			const uint8_t *vr = aux + (size_t)w * (h / 2 + y / 2);
			for (int x = 0; x < w; x++, o += 4) yuv_full_px(o, yr[x], ur[x], vr[x]);
		} else {
			// linha par: colunas pares da principal, ímpares do UV auxiliar
			const uint8_t *ac = auv + (size_t)w * (y / 2);
			for (int x = 0; x + 1 < w; x += 2, o += 8) {
				yuv_full_px(o, yr[x], mc[x], mc[x + 1]);
				yuv_full_px(o + 4, yr[x + 1], ac[x], ac[x + 1]);
			}
		}
	}
}

#endif
