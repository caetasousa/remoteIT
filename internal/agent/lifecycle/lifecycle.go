// Package lifecycle cuida de como o agente vive no sistema operacional: modo
// portátil ou serviço Windows, instância única e ajuste de DPI.
//
// No modo serviço, o processo do serviço roda na sessão 0, que nunca vê a
// tela. Por isso ele lança e vigia uma cópia do próprio exe ("worker") na
// sessão interativa ativa: a tela de login (Winlogon) ou a sessão do usuário.
// Só um processo dentro da sessão consegue capturar a tela e injetar input.
//
// Nada aqui abre janela: install/uninstall registram o resultado em
// %ProgramData%\RemoteIT\service.log (e no terminal, se houver), e o modo
// portátil sai em silêncio quando já há outra cópia ou o serviço rodando.
//
// Edições (Windows), do mesmo código, escolhidas no build:
//
//	suporte-ti.exe           portátil: dois cliques e roda, sem admin
//	suporte-ti-servico.exe   serviço: dois cliques = install (UAC)
//
// Subcomandos (Windows, nas duas edições):
//
//	suporte-ti.exe install     instala ou atualiza o serviço (pede o UAC se preciso)
//	suporte-ti.exe uninstall   para e remove o serviço (pede o UAC se preciso)
//	suporte-ti.exe service     execução interna, chamada pelo Windows
//	suporte-ti.exe worker      cópia que roda na sessão e captura a tela
package lifecycle

// Mode é como o processo atual foi iniciado.
type Mode int

const (
	// Portable é a execução direta pelo usuário (ou fora do Windows).
	Portable Mode = iota
	// Worker é a cópia lançada pelo serviço dentro da sessão interativa.
	Worker
	// Handled significa que um subcomando (install, uninstall, service) já
	// rodou e o processo deve sair.
	Handled
)
