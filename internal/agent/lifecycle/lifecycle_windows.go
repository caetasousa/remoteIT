//go:build windows

package lifecycle

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const (
	serviceName = "RemoteITAgent"
	serviceDesc = "RemoteIT - acesso remoto de suporte"
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	wtsapi32               = windows.NewLazySystemDLL("wtsapi32.dll")
	userenv                = windows.NewLazySystemDLL("userenv.dll")
	advapi32               = windows.NewLazySystemDLL("advapi32.dll")
	pSetProcessDPIAware    = user32.NewProc("SetProcessDPIAware")
	pCreateMutexW          = kernel32.NewProc("CreateMutexW")
	pWTSGetActiveConsole   = kernel32.NewProc("WTSGetActiveConsoleSessionId")
	pWTSQueryUserToken     = wtsapi32.NewProc("WTSQueryUserToken")
	pCreateEnvironmentBlk  = userenv.NewProc("CreateEnvironmentBlock")
	pDestroyEnvironmentBlk = userenv.NewProc("DestroyEnvironmentBlock")
	pCreateProcessAsUser   = advapi32.NewProc("CreateProcessAsUserW")
	pDuplicateTokenEx      = advapi32.NewProc("DuplicateTokenEx")
	pSetTokenInformation   = advapi32.NewProc("SetTokenInformation")
)

// Dispatch trata os subcomandos de args[1]. install, uninstall e service
// rodam aqui mesmo e devolvem [Handled]; "worker" devolve [Worker]; sem
// subcomando, [Portable] — ou, na edição de serviço (installer), install.
func Dispatch(args []string, installer bool) Mode {
	cmd := ""
	if len(args) > 1 {
		cmd = args[1]
	}
	switch cmd {
	case "install":
		install()
		return Handled
	case "uninstall":
		uninstall()
		return Handled
	case "service":
		runService()
		return Handled
	case "worker":
		return Worker
	}
	if installer && cmd == "" { // dois cliques na edição de serviço
		install()
		return Handled
	}
	return Portable
}

// Init prepara o processo antes de capturar a tela: ativa DPI awareness
// (coordenadas reais em telas com escala acima de 100%) e, no modo
// portátil, garante que só uma cópia do agente fale com o relay. Duas cópias
// com o mesmo ID se derrubariam lá. O worker é gerenciado pelo serviço e
// pula essa verificação.
//
// No modo portátil, sem nenhuma janela, sai em silêncio quando o serviço já
// está rodando (ele já dá o acesso, e a edição portátil nunca mexe nele) ou
// quando outra cópia portátil já está aberta na sessão.
func Init(mode Mode) {
	pSetProcessDPIAware.Call()
	if mode == Worker {
		// sobra de uma atualização anterior (veja ApplyUpdate); se o install
		// ainda estiver terminando, apagar falha e fica para a próxima
		os.RemoveAll(filepath.Join(StateDir(Worker), "update"))
		return
	}
	if serviceRunning() {
		os.Exit(0)
	}
	name, _ := syscall.UTF16PtrFromString(`Local\RemoteIT-Agent`)
	h, _, e := pCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h != 0 && e == syscall.ERROR_ALREADY_EXISTS {
		os.Exit(0)
	}
	instanceMutex = windows.Handle(h)
	cleanupOldExe()
	// o handle fica aberto até o processo terminar: é ele que mantém o mutex
}

// LogOutput devolve onde o agente deve escrever o log. O worker não tem
// console (é lançado sem janela pelo serviço), então escreve em
// %ProgramData%\RemoteIT\agent.log — o único jeito de saber, depois, por
// que a captura ou a entrada falharam no PC remoto. O arquivo recomeça
// quando passa de 2 MB. Nos outros modos, e se o arquivo não abrir, stderr.
func LogOutput(mode Mode) io.Writer {
	if mode != Worker {
		return os.Stderr
	}
	return openLog("agent.log")
}

// StateDir é onde o agente guarda a credencial da máquina. O worker do
// serviço (SYSTEM) usa %ProgramData%\RemoteIT, a mesma pasta dos logs; o
// portátil, que roda sem admin, usa %LOCALAPPDATA%\RemoteIT do usuário.
// São identidades separadas de propósito: o portátil não consegue ler a do
// serviço.
func StateDir(mode Mode) string {
	if mode == Worker {
		return filepath.Join(cmpEnv("ProgramData", `C:\ProgramData`), "RemoteIT")
	}
	if d, err := os.UserCacheDir(); err == nil { // %LOCALAPPDATA%
		return filepath.Join(d, "RemoteIT")
	}
	return filepath.Join(os.TempDir(), "RemoteIT")
}

func cmpEnv(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// openLog abre (criando) %ProgramData%\RemoteIT\name para acrescentar; o
// arquivo recomeça quando passa de 2 MB. Se não abrir, devolve stderr.
func openLog(name string) io.Writer {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	dir := filepath.Join(base, "RemoteIT")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return os.Stderr
	}
	path := filepath.Join(dir, name)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > 2<<20 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return os.Stderr
	}
	return f
}

type agentService struct{}

func (agentService) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptSessionChange
	s <- svc.Status{State: svc.StartPending}
	stop := make(chan struct{})
	go superviseSessions(stop)
	s <- svc.Status{State: svc.Running, Accepts: accepts}
	for req := range r {
		switch req.Cmd {
		case svc.Interrogate:
			s <- req.CurrentStatus
		case svc.Stop, svc.Shutdown:
			close(stop)
			killWorker()
			s <- svc.Status{State: svc.StopPending}
			return false, 0
		case svc.SessionChange:
			// Bloquear/desbloquear não muda a sessão: o worker (SYSTEM) segue
			// o desktop sozinho e a conexão do técnico continua. Login e
			// logout relançam para o worker pegar o ambiente (USERNAME) do
			// novo usuário; troca de sessão o supervisor detecta no tick.
			const wtsSessionLogon, wtsSessionLogoff = 5, 6
			if req.EventType == wtsSessionLogon || req.EventType == wtsSessionLogoff {
				relaunchWorker()
			}
		}
	}
	return false, 0
}

// svcLog é o log do processo do serviço (sessão 0, sem console):
// %ProgramData%\RemoteIT\service.log. Registra cada lançamento do worker e,
// principalmente, por que ele falhou.
var svcLog = slog.New(slog.NewTextHandler(io.Discard, nil))

// lastLaunchErr evita repetir no log o mesmo erro a cada tick; protegido por
// currentWorker.mu, como o resto de relaunchWorker.
var lastLaunchErr string

func runService() {
	svcLog = slog.New(slog.NewTextHandler(openLog("service.log"), nil))
	svcLog.Info("serviço iniciado")
	if isService, _ := svc.IsWindowsService(); isService {
		svc.Run(serviceName, agentService{})
	} else {
		superviseSessions(make(chan struct{})) // chamado fora do SCM: modo debug
	}
}

// currentWorker é usado pela goroutine do serviço (evento SessionChange) e
// pelo supervisor (tick de 2 s) ao mesmo tempo.
var currentWorker struct {
	mu      sync.Mutex
	handle  windows.Handle
	session uint32
	running bool
}

func lockWorker()   { currentWorker.mu.Lock() }
func unlockWorker() { currentWorker.mu.Unlock() }

const noSession = 0xFFFFFFFF

func activeSessionID() uint32 {
	r, _, _ := pWTSGetActiveConsole.Call()
	return uint32(r)
}

// superviseSessions mantém uma cópia do agente na sessão ativa. Além do
// evento SessionChange, confere a cada 2 s para pegar mudanças sem evento.
func superviseSessions(stop <-chan struct{}) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	relaunchWorker()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			sid := activeSessionID()
			if sid == noSession {
				continue
			}
			lockWorker()
			stale := !currentWorker.running || currentWorker.session != sid || !workerAliveLocked()
			unlockWorker()
			if stale {
				relaunchWorker()
			}
		}
	}
}

func workerAliveLocked() bool {
	var code uint32
	windows.GetExitCodeProcess(currentWorker.handle, &code)
	return code == 259 // STILL_ACTIVE
}

func killWorker() {
	lockWorker()
	defer unlockWorker()
	killWorkerLocked()
}

func killWorkerLocked() {
	if currentWorker.running && currentWorker.handle != 0 {
		windows.TerminateProcess(currentWorker.handle, 0)
		windows.CloseHandle(currentWorker.handle)
	}
	currentWorker.running, currentWorker.handle = false, 0
}

func relaunchWorker() {
	lockWorker()
	defer unlockWorker()
	killWorkerLocked()
	sid := activeSessionID()
	if sid == noSession {
		return
	}
	h, err := launchInSession(sid)
	if err != nil {
		if msg := err.Error(); msg != lastLaunchErr { // o tick repete a cada 2 s
			lastLaunchErr = msg
			svcLog.Error("não foi possível lançar o worker", "sessão", sid, "err", err)
		}
		return // tenta de novo no próximo tick
	}
	lastLaunchErr = ""
	pid, _ := windows.GetProcessId(h)
	svcLog.Info("worker lançado", "sessão", sid, "pid", pid)
	currentWorker.handle, currentWorker.session, currentWorker.running = h, sid, true
}

// launchInSession lança o próprio exe com o argumento "worker" dentro da
// sessão sid.
//
// O token é o do próprio serviço (SYSTEM), copiado e movido para a sessão
// sid — o mesmo arranjo de TeamViewer/AnyDesk. Com o token do usuário o
// worker teria integridade média, e o Windows (UIPI) descarta a entrada que
// ele injeta na tela de bloqueio/login, que roda como SYSTEM: a tela
// aparecia, mas nem clique nem tecla chegavam. Sem usuário logado nem
// existe token de usuário para usar.
//
// O ambiente (USERNAME, USERPROFILE) vem do usuário logado, quando há um,
// para o técnico ver quem está no PC.
//
// O STARTUPINFO aponta para "Default" só como ponto de partida; é o worker
// quem troca para o desktop certo em runtime (veja internal/agent/desktop).
func launchInSession(sid uint32) (windows.Handle, error) {
	var selfTok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ALL_ACCESS, &selfTok); err != nil {
		return 0, err
	}
	defer selfTok.Close()

	const (
		securityImpersonation = 2
		tokenPrimary          = 1
		maximumAllowed        = 0x02000000
		tokenSessionID        = 12 // TOKEN_INFORMATION_CLASS.TokenSessionId
	)
	var dupTok windows.Token
	if r, _, e := pDuplicateTokenEx.Call(uintptr(selfTok), maximumAllowed, 0,
		securityImpersonation, tokenPrimary, uintptr(unsafe.Pointer(&dupTok))); r == 0 {
		return 0, e
	}
	defer dupTok.Close()
	// mudar a sessão de um token exige SeTcbPrivilege, que o LocalSystem tem
	if r, _, e := pSetTokenInformation.Call(uintptr(dupTok), tokenSessionID,
		uintptr(unsafe.Pointer(&sid)), unsafe.Sizeof(sid)); r == 0 {
		return 0, e
	}

	envTok := dupTok
	var userTok windows.Token
	if r, _, _ := pWTSQueryUserToken.Call(uintptr(sid), uintptr(unsafe.Pointer(&userTok))); r != 0 {
		defer userTok.Close()
		envTok = userTok
	}
	var envBlock uintptr
	pCreateEnvironmentBlk.Call(uintptr(unsafe.Pointer(&envBlock)), uintptr(envTok), 0)
	defer func() {
		if envBlock != 0 {
			pDestroyEnvironmentBlk.Call(envBlock)
		}
	}()

	exe, _ := os.Executable()
	cmdline, _ := syscall.UTF16PtrFromString(`"` + exe + `" worker`)
	desktop, _ := syscall.UTF16PtrFromString(`winsta0\default`)

	type startupInfo struct {
		cb                    uint32
		_                     *uint16
		desktop               *uint16
		title                 *uint16
		x, y                  uint32
		xSize, ySize          uint32
		xCount, yCount        uint32
		fill                  uint32
		flags                 uint32
		showWindow            uint16
		_                     uint16
		_                     *byte
		stdIn, stdOut, stdErr windows.Handle
	}
	si := startupInfo{desktop: desktop}
	si.cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	const (
		createUnicodeEnvironment = 0x00000400
		createNoWindow           = 0x08000000
		detachedProcess          = 0x00000008
	)
	flags := uintptr(createUnicodeEnvironment | createNoWindow | detachedProcess)
	if r, _, e := pCreateProcessAsUser.Call(
		uintptr(dupTok), 0, uintptr(unsafe.Pointer(cmdline)),
		0, 0, 0, flags, envBlock, 0,
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi))); r == 0 {
		return 0, e
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}
