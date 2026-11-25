// Package win é a interface Windows do visualizador nativo do técnico
// (cmd/viewer): janela de login e lista de máquinas, e uma janela por sessão
// com o vídeo, o mouse e o teclado.
//
// É Win32 puro (sem dependências), numa thread do SO só (a do laço de
// mensagens). A rede e a decodificação rodam em goroutines e avisam a janela
// com PostMessage. Toda a lógica que não depende do Windows (API, protocolo,
// teclas) fica em internal/viewer/client, internal/protocol e
// internal/viewer/decode, testada no Linux.
package win
