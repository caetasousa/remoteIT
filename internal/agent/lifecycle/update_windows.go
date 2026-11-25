//go:build windows

package lifecycle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// instanceMutex é o mutex de instância única do portátil. A atualização o
// solta antes de abrir a versão nova, senão ela acharia "já em execução".
var instanceMutex windows.Handle

// ApplyUpdate troca este agente pela versão exe (já conferida pelo pacote
// update) e encerra o processo atual, direta ou indiretamente.
//
//   - Worker (serviço): grava o .exe novo em %ProgramData%\RemoteIT\update e
//     o executa com "install". É o mesmo caminho de "baixar e dar dois
//     cliques": o install para o serviço (que encerra este worker), copia o
//     .exe para o Program Files e inicia o serviço de novo. O worker roda
//     como SYSTEM, então o install já nasce com permissão, sem UAC.
//   - Portátil: o Windows deixa renomear um .exe em uso. O atual vira
//     .old, o novo ocupa o nome dele e é aberto com os mesmos argumentos.
func ApplyUpdate(mode Mode, exe []byte) error {
	if mode == Worker {
		dir := filepath.Join(StateDir(Worker), "update")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		path := filepath.Join(dir, "suporte-ti.exe")
		if err := os.WriteFile(path, exe, 0o755); err != nil {
			return err
		}
		cmd := exec.Command(path, "install")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		return cmd.Start() // o install derruba este processo ao parar o serviço
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	old := self + ".old"
	os.Remove(old)
	if err := os.Rename(self, old); err != nil {
		return err
	}
	if err := os.WriteFile(self, exe, 0o755); err != nil {
		os.Rename(old, self) // volta a versão atual
		return err
	}
	if instanceMutex != 0 {
		windows.CloseHandle(instanceMutex)
	}
	cmd := exec.Command(self, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		return errors.Join(err, os.Rename(old, self))
	}
	os.Exit(0)
	return nil
}

// cleanupOldExe apaga o .old que uma atualização do portátil deixou.
func cleanupOldExe() {
	if self, err := os.Executable(); err == nil {
		os.Remove(self + ".old")
	}
}
