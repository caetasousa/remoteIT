// Package dpapi protege segredos guardados em disco com o DPAPI do Windows:
// só esta máquina (machineScope) ou só este usuário nesta máquina consegue
// abrir. Usado pela credencial do agente (internal/agent/machine) e pela
// sessão do visualizador do técnico (cmd/viewer). Fora do Windows não cifra
// (o arquivo fica com permissão 0600).
package dpapi
