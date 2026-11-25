// Command viewer é o visualizador nativo do técnico para Windows
// (remoteit-tecnico.exe): login, lista das máquinas e uma janela por sessão,
// com decodificação H.264 na GPU e todas as teclas (Win, Alt+Tab) sem
// precisar de tela cheia. Usa a mesma conta, a mesma API e o mesmo protocolo
// do console no navegador, que continua existindo (docs/adr/0012-visualizador-nativo.md).
//
// Configurações em %LOCALAPPDATA%\RemoteIT: viewer.json (relay e usuário) e
// viewer.token (a sessão, protegida pelo DPAPI do usuário).
package main

// Embutidos pelo build.sh.
var (
	relayURL        = "https://suporte.exemplo.com"
	version         = ""
	updatePublicKey = ""
)

func main() { run() }
