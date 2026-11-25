// Package gpu é a fonte de vídeo rápida do agente no Windows: captura com
// DXGI Desktop Duplication e codifica na GPU via Media Foundation, em três
// codecs:
//
//   - [NewH264]: H.264 4:2:0, o padrão; decodificado em qualquer navegador.
//   - [NewAVC444]: cor 4:4:4 em dois fluxos H.264 (veja internal/avc444),
//     para o visualizador nativo.
//   - [NewAV1]: AV1, só com encoder AV1 de GPU; menos banda com texto.
//
// A implementação está em C (rcenc.h) e é chamada via CGO. Só existe em
// builds Windows com CGO, feitos pelo build.sh com Zig como compilador C
// cruzado; nos demais builds as fábricas sempre devolvem erro e o agente usa
// JPEG.
//
// Pipeline (tudo na GPU):
//
//	DXGI Desktop Duplication (só entrega quadro quando a tela muda)
//	  -> D3D11 Video Processor (escala + BGRA->NV12 BT.709)  [H.264, AV1]
//	  -> ou shaders D3D11 que montam as duas visões NV12     [AVC444]
//	  -> Media Foundation (NVENC / Intel QSV / AMD AMF)
//	  -> Annex-B (H.264) ou OBUs (AV1), decodificados via WebCodecs ou
//	     Media Foundation no visualizador nativo
package gpu
