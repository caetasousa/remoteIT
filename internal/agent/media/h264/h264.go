// Package h264 é a fonte de vídeo rápida do agente no Windows: captura com
// DXGI Desktop Duplication e codifica H.264 na GPU via Media Foundation.
//
// A implementação está em C (rcenc.h) e é chamada via CGO. Só existe em
// builds Windows com CGO, feitos pelo build.sh com Zig como compilador C
// cruzado; nos demais builds [New] sempre devolve erro e o agente usa JPEG.
//
// Pipeline (tudo na GPU quando há encoder de hardware):
//
//	DXGI Desktop Duplication (só entrega quadro quando a tela muda)
//	  -> D3D11 Video Processor (escala + BGRA->NV12 BT.709)
//	  -> Media Foundation H.264 (NVENC / Intel QSV / AMD AMF; fallback software)
//	  -> Annex-B H.264, decodificado no navegador via WebCodecs
package h264
