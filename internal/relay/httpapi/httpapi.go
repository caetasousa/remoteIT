// Package httpapi expõe o relay por HTTP e WebSocket: API JSON do console,
// conexões de agentes e técnicos, download do agente e os arquivos do
// frontend.
//
// Este pacote só traduz HTTP para chamadas aos contextos [hub],
// [identity] e [audit]; as regras ficam neles.
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
//	GET    /api/agents          máquinas online
//	GET    /api/users           admin: lista usuários
//	POST   /api/users           admin: cadastra usuário
//	POST   /api/users/{id}/approve  admin: aprova conta pendente
//	DELETE /api/users/{id}      admin: remove usuário
//	GET    /api/audit           admin: últimos acessos
//	GET    /ws/viewer?id=       WebSocket do técnico
//	GET    /ws/agent?key=&id=   WebSocket do agente (autenticado pela chave)
//	GET    /download/agent.exe          agente portátil (só executa), se configurado
//	GET    /download/agent-servico.exe  agente que instala o serviço, se configurado
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
)

// Config reúne as dependências do servidor HTTP.
type Config struct {
	Hub      *hub.Hub
	Identity *identity.Service
	Audit    *audit.Log
	// Accounts é nil quando não há SMTP: cadastro e recuperação respondem 503.
	Accounts *identity.Accounts
	// AgentKey é a chave compartilhada que os agentes enviam em ?key=.
	AgentKey string
	// AgentExe é o caminho do .exe servido em /download/agent.exe. Vazio
	// desativa a rota.
	AgentExe string
	// AgentServiceExe é o .exe da edição de serviço, servido em
	// /download/agent-servico.exe. Vazio desativa a rota.
	AgentServiceExe string
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
	upgrader   websocket.Upgrader
	emailLimit *rateLimit
}

// New monta o handler HTTP do relay.
func New(cfg Config) http.Handler {
	s := &server{Config: cfg, emailLimit: newRateLimit(emailRequestsPerHour, time.Hour), upgrader: websocket.Upgrader{
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
	mux.Handle("GET /api/agents", s.auth(http.HandlerFunc(s.listAgents)))
	mux.Handle("GET /api/users", s.admin(http.HandlerFunc(s.listUsers)))
	mux.Handle("POST /api/users", s.admin(http.HandlerFunc(s.createUser)))
	mux.Handle("DELETE /api/users/{id}", s.admin(http.HandlerFunc(s.deleteUser)))
	mux.Handle("GET /api/audit", s.admin(http.HandlerFunc(s.listAudit)))
	mux.Handle("GET /ws/viewer", s.auth(http.HandlerFunc(s.viewerWS)))
	mux.HandleFunc("GET /ws/agent", s.agentWS)
	if cfg.AgentExe != "" {
		mux.Handle("GET /download/agent.exe", s.auth(download(cfg.AgentExe, "suporte-ti.exe")))
	}
	if cfg.AgentServiceExe != "" {
		mux.Handle("GET /download/agent-servico.exe", s.auth(download(cfg.AgentServiceExe, "suporte-ti-servico.exe")))
	}
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /", staticHandler(cfg.Static))
	return mux
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

// download serve o arquivo em path com o nome name para o navegador.
func download(path, name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		// Sem isso o Cloudflare (que guarda .exe por padrão) e o navegador
		// continuavam entregando um agente antigo depois de cada deploy.
		// private também impede que um cache compartilhado guarde um arquivo
		// que só sai para quem está logado.
		w.Header().Set("Cache-Control", "no-store, private")
		http.ServeFile(w, r, path)
	})
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
