// Command relay é o servidor público do RemoteIT. Agentes e técnicos se
// conectam a ele pela porta 443 (saída), e ele faz a ponte entre os dois,
// serve o console web e guarda usuários e auditoria no PostgreSQL.
//
// Uso:
//
//	relay [-addr :443] (-domain host | -cert f -key f | -insecure) [-agent-exe caminho] [-agent-service-exe caminho]
//	relay -healthcheck [-addr :8080]
//
// Variáveis de ambiente:
//
//	DATABASE_URL       obrigatória, ex.: postgres://user:pass@db:5432/remoteit
//	RELAY_AGENT_KEY    obrigatória, >= 16 caracteres: a chave dos agentes legados
//	                   (sem cadastro), a mesma embutida no agente pelo build.sh
//	RELAY_LEGACY_AGENTS "false" recusa agentes legados; padrão: aceita, visíveis
//	                   só para administradores, até as máquinas serem reinstaladas
//	RELAY_ADMIN_USER   nome do admin inicial (padrão "admin")
//	RELAY_ADMIN_PASS   senha do admin inicial; só é usada enquanto não existe
//	                   nenhum usuário no banco
//	RELAY_PUBLIC_URL   base dos links dos e-mails e do script de instalação, ex.: https://suporte.exemplo.com
//	RELAY_ADMIN_EMAIL  quem confirmar esta caixa no cadastro vira admin aprovado
//	SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASSWORD, SMTP_STARTTLS,
//	EMAIL_REMETENTE, EMAIL_REMETENTE_NOME
//	                   ligam cadastro e recuperação por e-mail
//	RELAY_CLIENT_IP_HEADER  cabeçalho com o IP real do técnico para a auditoria
//	                   (ex.: Cf-Connecting-IP). Só use se o relay for acessível
//	                   apenas pelo proxy/túnel que define esse cabeçalho.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/httpapi"
	"remoteit/internal/relay/hub"
	"remoteit/internal/relay/identity"
	"remoteit/internal/relay/machines"
	"remoteit/internal/relay/mail"
	"remoteit/internal/relay/server"
	"remoteit/internal/relay/storage/postgres"
	"remoteit/internal/relay/web"
)

func main() {
	var opt server.Options
	flag.StringVar(&opt.Addr, "addr", ":443", "endereço de escuta")
	flag.StringVar(&opt.Domain, "domain", "", "domínio para TLS automático Let's Encrypt (precisa da porta 443)")
	flag.StringVar(&opt.CertCacheDir, "cert-cache", "certs", "pasta dos certificados do -domain")
	flag.StringVar(&opt.CertFile, "cert", "", "certificado TLS (alternativa ao -domain)")
	flag.StringVar(&opt.KeyFile, "key", "", "chave TLS")
	flag.BoolVar(&opt.Insecure, "insecure", false, "HTTP sem TLS (testes ou atrás de proxy reverso)")
	agentExe := flag.String("agent-exe", "", "caminho do agente portátil servido em /download/agent.exe")
	agentSvcExe := flag.String("agent-service-exe", "",
		"caminho do agente de serviço servido em /download/agent-servico.exe (padrão: suporte-ti-servico.exe ao lado do -agent-exe)")
	healthcheck := flag.Bool("healthcheck", false, "consulta /healthz em -addr e sai com 0 se saudável (para Docker)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if *healthcheck {
		os.Exit(runHealthcheck(opt.Addr))
	}
	if *agentSvcExe == "" && *agentExe != "" {
		*agentSvcExe = filepath.Join(filepath.Dir(*agentExe), "suporte-ti-servico.exe")
	}
	if err := run(opt, *agentExe, *agentSvcExe, logger); err != nil {
		logger.Error("relay encerrado com erro", "err", err)
		os.Exit(1)
	}
}

func run(opt server.Options, agentExe, agentSvcExe string, logger *slog.Logger) error {
	dbURL, agentKey := os.Getenv("DATABASE_URL"), os.Getenv("RELAY_AGENT_KEY")
	if dbURL == "" || len(agentKey) < 16 {
		return fmt.Errorf("defina DATABASE_URL e RELAY_AGENT_KEY (>= 16 caracteres)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := postgres.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return err
	}

	ids := identity.NewService(db)
	accounts, err := newAccounts(db, logger)
	if err != nil {
		return err
	}
	if accounts != nil {
		go accounts.Run(ctx)
	}
	adminUser := cmp(os.Getenv("RELAY_ADMIN_USER"), "admin")
	adminPass := os.Getenv("RELAY_ADMIN_PASS")
	// sem senha no ambiente, o primeiro admin se cadastra pelo RELAY_ADMIN_EMAIL
	if adminPass != "" || accounts == nil || os.Getenv("RELAY_ADMIN_EMAIL") == "" {
		created, err := ids.EnsureAdmin(ctx, adminUser, adminPass)
		if err != nil {
			return fmt.Errorf("criar admin inicial %q (confira RELAY_ADMIN_PASS, ou use RELAY_ADMIN_EMAIL com SMTP): %w", adminUser, err)
		}
		if created {
			logger.Info("admin inicial criado", "user", adminUser)
		}
	}

	auditLog := audit.New(db, logger)
	if n, err := auditLog.CloseOrphans(ctx); err != nil {
		return fmt.Errorf("encerrar acessos abertos por um reinício: %w", err)
	} else if n > 0 {
		logger.Info("acessos encerrados pelo reinício do relay", "quantos", n)
	}
	legacy := os.Getenv("RELAY_LEGACY_AGENTS") != "false"
	if legacy {
		logger.Info("agentes legados (chave compartilhada) aceitos; RELAY_LEGACY_AGENTS=false os recusa")
	}

	handler := httpapi.New(httpapi.Config{
		Hub:             hub.New(),
		Identity:        ids,
		Audit:           auditLog,
		Accounts:        accounts,
		Machines:        machines.New(db),
		AgentKey:        agentKey,
		LegacyAgents:    legacy,
		AgentExe:        agentExe,
		AgentServiceExe: agentSvcExe,
		PublicURL:       strings.TrimRight(os.Getenv("RELAY_PUBLIC_URL"), "/"),
		LocalURL:        strings.TrimRight(os.Getenv("RELAY_LOCAL_URL"), "/"),
		UpdateManifest:  updateManifest(agentExe),
		ViewerExe:       sibling(agentExe, "remoteit-tecnico.exe"),
		Static:          web.FS(),
		Health:          db.Ping,
		Logger:          logger,

		ClientIPHeader: os.Getenv("RELAY_CLIENT_IP_HEADER"),
	})
	return server.Run(ctx, opt, handler, logger)
}

// sibling é o arquivo name na pasta do agente ("" sem agente configurado).
func sibling(agentExe, name string) string {
	if agentExe == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(agentExe), name)
}

// updateManifest é o update.json que o build.sh grava ao lado dos .exe.
func updateManifest(agentExe string) string {
	if agentExe == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(agentExe), "update.json")
}

// newAccounts devolve nil, sem erro, quando não há SMTP configurado.
func newAccounts(db *postgres.DB, logger *slog.Logger) (*identity.Accounts, error) {
	cfg, enabled, err := mail.ConfigFromEnv(os.Getenv)
	if err != nil {
		return nil, err
	}
	if !enabled {
		logger.Info("sem SMTP_HOST: cadastro e recuperação por e-mail desligados")
		return nil, nil
	}
	sender, err := mail.New(cfg)
	if err != nil {
		return nil, err
	}
	return identity.NewAccounts(db, sender, os.Getenv("RELAY_ADMIN_EMAIL"), logger)
}

func cmp(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// runHealthcheck existe porque a imagem distroless não tem curl nem wget.
func runHealthcheck(addr string) int {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
