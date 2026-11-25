// Package httpapi expõe o relay por HTTP e WebSocket: API JSON do console,
// conexões de agentes e técnicos, download do agente e os arquivos do
// frontend.
//
// Este pacote só traduz HTTP para chamadas aos contextos [hub],
// [identity], [machines] e [audit]; as regras ficam neles.
//
// Rotas:
//
//	POST   /api/login           público: abre sessão (cookie)
//	POST   /api/logout          encerra a sessão
//	GET    /api/me              usuário logado
//	GET    /api/account/config            público: cadastro por e-mail ligado?
//	POST   /api/account/signup            público: pede link de cadastro
//	POST   /api/account/signup/complete   público + link: cria a conta
//	POST   /api/account/reset             público: pede link de nova senha
//	POST   /api/account/reset/complete    público + link: troca a senha
//	POST   /api/account/email             pede link para associar e-mail
//	POST   /api/account/email/complete    link: associa o e-mail
//	GET    /api/agents          máquinas que o usuário acessa (online e offline)
//	DELETE /api/machines/{id}   remove (revoga) a máquina
//	GET    /api/links           links de instalação válidos do técnico
//	POST   /api/links           cria um link de instalação
//	DELETE /api/links/{id}      revoga um link
//	POST   /api/agent/enroll    público + código: cadastra uma máquina
//	GET    /api/agent/update?mid=&edition=  máquina: versão disponível (assinada)
//	GET    /api/agent/update/{edition}?mid= máquina: .exe da atualização
//	GET    /api/users           admin: lista usuários
//	POST   /api/users           admin: cadastra usuário
//	POST   /api/users/{id}/approve  admin: aprova conta pendente
//	DELETE /api/users/{id}      admin: remove usuário
//	GET    /api/audit           admin: últimos acessos
//	GET    /ws/viewer?id=       WebSocket do técnico
//	GET    /ws/agent?mid=       WebSocket do agente (credencial da máquina no Authorization)
//	GET    /ws/agent?key=&id=   WebSocket de agente legado (chave compartilhada)
//	GET    /download/agent.exe          agente portátil, marcado com um código do técnico
//	GET    /download/agent-servico.exe  agente que instala o serviço, marcado
//	GET    /i/{code}            público: .exe de um link de instalação
//	GET    /i/{code}/ps         público: o mesmo link como script PowerShell (irm … | iex)
//	GET    /download/viewer.exe visualizador nativo do técnico (Windows)
//	GET    /api/viewer/update   versão disponível do visualizador (assinada)
//	GET    /api/viewer/update.exe  o visualizador, para a atualização
//	GET    /healthz             público: relay e banco respondendo
//	GET    /                    arquivos do console (Svelte)
package httpapi

import (
	"context"
	"crypto/subtle"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/hub"
	"remoteit/internal/relay/identity"
	"remoteit/internal/relay/machines"
)

// Config reúne as dependências do servidor HTTP.
type Config struct {
	Hub      *hub.Hub
	Identity *identity.Service
	Audit    *audit.Log
	// Accounts é nil quando não há SMTP: cadastro e recuperação respondem 503.
	Accounts *identity.Accounts
	Machines *machines.Service
	// AgentKey é a chave compartilhada dos agentes legados (?key=). Vazia, ou
	// com LegacyAgents falso, só máquinas cadastradas conectam.
	AgentKey string
	// LegacyAgents aceita agentes antigos, sem cadastro, enquanto as máquinas
	// não são reinstaladas. Eles aparecem só para administradores.
	LegacyAgents bool
	// AgentExe é o caminho do .exe portátil, servido (marcado com o código do
	// técnico) em /download/agent.exe e pelos links de instalação.
	AgentExe string
	// AgentServiceExe é o .exe da edição de serviço, servido em
	// /download/agent-servico.exe. Vazio desativa a rota.
	AgentServiceExe string
	// ViewerExe é o visualizador nativo do técnico (remoteit-tecnico.exe),
	// servido em /download/viewer.exe e /api/viewer/update.exe.
	ViewerExe string
	// UpdateManifest é o update.json do build.sh (versão e assinaturas dos
	// .exe). Vazio ou ausente: nenhuma atualização é oferecida.
	UpdateManifest string
	// PublicURL é a origem pública do relay (RELAY_PUBLIC_URL), usada no
	// script de instalação por PowerShell. Vazia: vem da requisição.
	PublicURL string
	// Static contém o build do frontend, com index.html na raiz.
	Static fs.FS
	// Health confere dependências externas (o banco). Nil = sempre saudável.
	Health func(context.Context) error
	// ClientIPHeader, se definido, é o cabeçalho com o IP real do técnico
	// gravado na auditoria (ex.: Cf-Connecting-IP). Só é seguro quando o
	// relay é acessível apenas pelo proxy/túnel que define esse cabeçalho;
	// exposto direto, qualquer um poderia falsificá-lo.
	ClientIPHeader string
	Logger         *slog.Logger
}

type server struct {
	Config
	upgrader    websocket.Upgrader
	emailLimit  *rateLimit
	loginLimit  *failureLimit
	enrollLimit *rateLimit
	linkLimit   *rateLimit
}

// New monta o handler HTTP do relay.
func New(cfg Config) http.Handler {
	s := &server{Config: cfg,
		emailLimit:  newRateLimit(emailRequestsPerHour, time.Hour),
		loginLimit:  newFailureLimit(),
		enrollLimit: newRateLimit(enrollsPerHour, time.Hour),
		linkLimit:   newRateLimit(linkDownloadsPerDay, 24*time.Hour),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  64 << 10,
			WriteBufferSize: 64 << 10,
			// CheckOrigin padrão: só aceita Origin igual ao Host. Como o técnico é
			// autenticado por cookie, isso impede que outro site abra um WebSocket
			// em nome dele (Cross-Site WebSocket Hijacking). O agente não envia
			// Origin e por isso passa.
		}}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.Handle("GET /api/me", s.auth(http.HandlerFunc(s.me)))
	mux.HandleFunc("GET /api/account/config", s.accountConfig)
	mux.HandleFunc("POST /api/account/signup", s.emailRoute(s.requestSignup))
	mux.HandleFunc("POST /api/account/signup/complete", s.accountRoute(s.completeSignup))
	mux.HandleFunc("POST /api/account/reset", s.emailRoute(s.requestReset))
	mux.HandleFunc("POST /api/account/reset/complete", s.accountRoute(s.resetPassword))
	mux.Handle("POST /api/account/email", s.auth(s.emailRoute(s.requestLink)))
	mux.Handle("POST /api/account/email/complete", s.auth(s.accountRoute(s.confirmLink)))
	mux.Handle("POST /api/users/{id}/approve", s.admin(http.HandlerFunc(s.approveUser)))
	mux.Handle("GET /api/agents", s.auth(http.HandlerFunc(s.listMachines)))
	mux.Handle("DELETE /api/machines/{id}", s.auth(http.HandlerFunc(s.removeMachine)))
	mux.Handle("GET /api/links", s.auth(http.HandlerFunc(s.listLinks)))
	mux.Handle("POST /api/links", s.auth(http.HandlerFunc(s.createLink)))
	mux.Handle("DELETE /api/links/{id}", s.auth(http.HandlerFunc(s.revokeLink)))
	mux.HandleFunc("POST /api/agent/enroll", s.enrollAgent)
	mux.HandleFunc("GET /api/agent/update", s.updateOffer)
	mux.HandleFunc("GET /api/agent/update/{edition}", s.updateDownload)
	mux.Handle("GET /api/viewer/update", s.auth(http.HandlerFunc(s.viewerOffer)))
	mux.Handle("GET /api/viewer/update.exe", s.auth(http.HandlerFunc(s.viewerExe)))
	mux.Handle("GET /download/viewer.exe", s.auth(http.HandlerFunc(s.viewerExe)))
	mux.Handle("GET /api/users", s.admin(http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/users", s.admin(http.HandlerFunc(s.createUser)))
	mux.Handle("DELETE /api/users/{id}", s.admin(http.HandlerFunc(s.deleteUser)))
	mux.Handle("GET /api/audit", s.admin(http.HandlerFunc(s.listAudit)))
	mux.Handle("GET /ws/viewer", s.auth(http.HandlerFunc(s.viewerWS)))
	mux.HandleFunc("GET /ws/agent", s.agentWS)
	if cfg.AgentExe != "" {
		mux.Handle("GET /download/agent.exe", s.auth(s.download(machines.EditionPortable)))
	}
	if cfg.AgentServiceExe != "" {
		mux.Handle("GET /download/agent-servico.exe", s.auth(s.download(machines.EditionService)))
	}
	mux.HandleFunc("GET /i/{code}", s.installLink)
	mux.HandleFunc("GET /i/{code}/ps", s.installScript)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /", staticHandler(cfg.Static))
	return securityHeaders(mux)
}

// securityHeaders aplica a todas as respostas as defesas do navegador: CSP
// (só scripts e conexões do próprio relay), nada de iframe de outros sites
// (clickjacking), nada de adivinhar o tipo do arquivo e nada de vazar a URL
// (que pode ter um link de uso único) para outros sites.
//
// style-src aceita 'unsafe-inline' porque o Svelte aplica estilos dinâmicos
// em atributos style (tamanho do canvas, posição do cursor remoto); scripts
// inline continuam proibidos, que é o que importa contra XSS.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; font-src 'self' data:; " +
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; connect-src 'self'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// o WebSocket da tela remota vai para o próprio host; alguns
		// navegadores não contam ws/wss como 'self'. Ecoar o Host aqui é
		// seguro: a CSP só restringe, nunca libera algo além do que já liberava.
		connect := ""
		if host := r.Host; host != "" && !strings.ContainsAny(host, " ;,'\"") {
			connect = " wss://" + host + " ws://" + host
		}
		h.Set("Content-Security-Policy", csp+connect)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	if s.Health != nil {
		if err := s.Health(r.Context()); err != nil {
			http.Error(w, "banco indisponível", http.StatusServiceUnavailable)
			return
		}
	}
	w.Write([]byte("ok"))
}

// staticHandler serve o build do Vite. index.html nunca vai para cache (o
// console reflete a versão atual logo após um deploy); os arquivos em
// assets/ têm hash no nome e podem ficar em cache para sempre.
func staticHandler(static fs.FS) http.Handler {
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func equalSecret(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
