// Package lock bloqueia a estação de trabalho a pedido do técnico.
//
// Win+L não pode ser simulado por SendInput: o Windows recusa de propósito
// essa combinação vinda de entrada sintética, porque ela trocaria para a
// estação de janela segura (Winlogon) — a mesma trava que impede automação
// maliciosa de bloqueio de sessão. A chamada direta a LockWorkStation, por
// rodar como código do processo e não como evento de teclado, não esbarra
// nessa trava e tem o mesmo efeito.
package lock

// Station bloqueia a estação de trabalho (equivalente a Win+L). Fora do
// Windows não faz nada; veja lock_windows.go e lock_other.go.
