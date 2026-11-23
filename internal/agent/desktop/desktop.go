// Package desktop mantém a thread de captura anexada ao desktop do Windows
// que está recebendo entrada do usuário agora — "Default" durante o uso
// normal, "Winlogon" na tela de bloqueio/login.
//
// Esse é o bug que este pacote corrige: o worker lançado pelo serviço (veja
// internal/agent/lifecycle) tinha o token da sessão certa, mas toda thread
// nova do Windows começa anexada ao desktop "Default". GDI e DXGI só
// enxergam o desktop ao qual a thread chamadora está anexada; presos no
// "Default" durante o bloqueio, eles capturavam o desktop errado (vazio) em
// vez do "Winlogon", onde a tela de login é desenhada de verdade.
package desktop

// FollowInput muda a thread atual (runtime.LockOSThread já deve ter sido
// chamado) para o desktop que está recebendo entrada do usuário agora.
// Idempotente: já estando no desktop certo, não faz nada. Fora do Windows
// não faz nada.
//
// Deve ser chamado antes de qualquer GetDC/criação de recurso gráfico na
// mesma thread, e de novo sempre que a captura falhar — a troca de desktop
// (bloquear, logar, Ctrl+Alt+Del) não gera nenhum evento que esta thread
// veja: só uma falha de captura revela que o desktop mudou debaixo dela.
func FollowInput() error { return followInput() }

// InputName é o nome do desktop que recebe entrada agora ("Default" ou, na
// tela de bloqueio/login e no UAC, "Winlogon"). Só para log; vazio fora do
// Windows.
func InputName() string { return inputName() }
