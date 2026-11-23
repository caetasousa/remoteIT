//go:build windows

package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Instalação e remoção do serviço sem nenhuma janela: o usuário raramente
// lê caixas de mensagem e toma qualquer aviso por defeito. O resultado vai
// para %ProgramData%\RemoteIT\service.log e, se o comando veio de um
// terminal, também para ele. A única janela possível é o UAC do próprio
// Windows, quando falta permissão de administrador.
//
// install é idempotente e serve também para atualizar: copia o exe para
// %ProgramFiles%\RemoteIT (assim o serviço nunca depende do arquivo em
// Downloads, que o navegador renomeia e o usuário apaga), para o serviço se
// ele já existir, troca o exe e inicia de novo.

var pAttachConsole = kernel32.NewProc("AttachConsole")

func installedExe() string {
	base := os.Getenv("ProgramFiles")
	if base == "" {
		base = `C:\Program Files`
	}
	return filepath.Join(base, "RemoteIT", "suporte-ti.exe")
}

// reporter registra o andamento no log e, se houver, no terminal que chamou.
type reporter struct {
	log     *slog.Logger
	console io.Writer
}

func newReporter(cmd string) *reporter {
	r := &reporter{log: slog.New(slog.NewTextHandler(openLog("service.log"), nil)).With("cmd", cmd)}
	// o exe é de interface gráfica (sem console próprio); se veio de um
	// PowerShell/cmd, escreve no console dele
	const attachParentProcess = ^uintptr(0)
	if ok, _, _ := pAttachConsole.Call(attachParentProcess); ok != 0 {
		if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
			r.console = f
		}
	}
	return r
}

func (r *reporter) info(msg string, args ...any) {
	r.log.Info(msg, args...)
	if r.console != nil {
		fmt.Fprintln(r.console, "RemoteIT:", msg)
	}
}

func (r *reporter) fail(msg string, err error) {
	r.log.Error(msg, "err", err)
	if r.console != nil {
		fmt.Fprintf(r.console, "RemoteIT: ERRO: %s: %v\n", msg, err)
	}
	os.Exit(1)
}

func elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// relaunchElevated roda o próprio exe com arg pedindo elevação (UAC).
func relaunchElevated(arg string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	args, _ := windows.UTF16PtrFromString(arg)
	return windows.ShellExecute(0, verb, file, args, nil, windows.SW_HIDE)
}

func install() {
	r := newReporter("install")
	if !elevated() {
		r.info("pedindo permissão de administrador")
		if err := relaunchElevated("install"); err != nil {
			r.fail("permissão de administrador negada", err)
		}
		return
	}
	m, err := mgr.Connect()
	if err != nil {
		r.fail("gerenciador de serviços", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	exists := err == nil
	if exists {
		defer s.Close()
		if err := stopAndWait(s); err != nil {
			r.fail("não foi possível parar o serviço anterior", err)
		}
	}

	dst, err := placeExe()
	if err != nil {
		if exists {
			s.Start() // volta a versão anterior a rodar: melhor do que nada
		}
		r.fail("não foi possível copiar o agente para "+filepath.Dir(installedExe()), err)
	}

	if exists {
		cfg, err := s.Config()
		if err != nil {
			r.fail("ler configuração do serviço", err)
		}
		cfg.BinaryPathName = `"` + dst + `" service`
		cfg.StartType = mgr.StartAutomatic
		if err := s.UpdateConfig(cfg); err != nil {
			r.fail("atualizar o serviço", err)
		}
	} else {
		s, err = m.CreateService(serviceName, dst, mgr.Config{
			DisplayName: serviceDesc,
			Description: serviceDesc,
			StartType:   mgr.StartAutomatic,
			ServiceType: windows.SERVICE_WIN32_OWN_PROCESS,
		}, "service")
		if err != nil {
			r.fail("criar o serviço", err)
		}
		defer s.Close()
	}
	s.SetRecoveryActions([]mgr.RecoveryAction{ // reinício automático em caso de falha
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
	}, 86400)
	if err := s.Start(); err != nil {
		r.fail("iniciar o serviço", err)
	}
	if exists {
		r.info("serviço atualizado e iniciado", "exe", dst)
	} else {
		r.info("serviço instalado e iniciado", "exe", dst)
	}
}

func uninstall() {
	r := newReporter("uninstall")
	if !elevated() {
		r.info("pedindo permissão de administrador")
		if err := relaunchElevated("uninstall"); err != nil {
			r.fail("permissão de administrador negada", err)
		}
		return
	}
	m, err := mgr.Connect()
	if err != nil {
		r.fail("gerenciador de serviços", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		r.info("o serviço não estava instalado")
		return
	}
	defer s.Close()
	if err := stopAndWait(s); err != nil {
		r.fail("parar o serviço", err)
	}
	if err := s.Delete(); err != nil {
		r.fail("remover o serviço", err)
	}
	// o exe instalado só sai se não for ele mesmo que está rodando agora
	if self, _ := os.Executable(); !sameFile(self, installedExe()) {
		os.RemoveAll(filepath.Dir(installedExe()))
	}
	r.info("serviço removido")
}

// stopAndWait para o serviço e espera ele (e o worker, que ele encerra ao
// parar) sair de fato, para o exe poder ser substituído em seguida.
func stopAndWait(s *mgr.Service) error {
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil && st.State == svc.Running {
			return err
		}
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if st, err = s.Query(); err == nil && st.State == svc.Stopped {
			return nil
		}
	}
	return errors.New("o serviço não parou em 20 s")
}

// placeExe copia o exe atual para installedExe e devolve o destino. Grava ao
// lado e renomeia por cima, para nunca deixar um exe pela metade; tenta por
// alguns segundos porque o Windows demora um pouco a soltar o arquivo do
// processo que acabou de sair.
func placeExe() (string, error) {
	dst := installedExe()
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if sameFile(self, dst) {
		return dst, nil
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return "", err
	}
	for i := 0; ; i++ {
		err = os.Rename(tmp, dst)
		if err == nil || i == 25 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dst, nil
}

func sameFile(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// serviceRunning diz se o serviço está rodando. Funciona sem admin: só pede
// direito de consulta.
func serviceRunning() bool {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(h)
	name, _ := windows.UTF16PtrFromString(serviceName)
	sh, err := windows.OpenService(h, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(sh)
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(sh, &st) != nil {
		return false
	}
	return st.CurrentState == windows.SERVICE_RUNNING
}
