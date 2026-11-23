// Package server sobe o servidor HTTP do relay em um dos três modos de TLS e
// o encerra de forma limpa quando o contexto é cancelado.
package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// Options escolhe endereço e modo de TLS. Exatamente um modo deve ser usado:
// Domain (Let's Encrypt automático), CertFile+KeyFile, ou Insecure.
type Options struct {
	Addr string
	// Domain ativa TLS automático via Let's Encrypt; exige a porta 443
	// acessível pela internet. Os certificados ficam em CertCacheDir.
	Domain       string
	CertCacheDir string
	CertFile     string
	KeyFile      string
	// Insecure serve HTTP puro: para testes ou atrás de proxy reverso/túnel
	// que termina o TLS (Cloudflare Tunnel, nginx, Caddy).
	Insecure bool
}

// ErrNoTLSMode é devolvido por [Run] quando nenhum modo de TLS foi escolhido.
var ErrNoTLSMode = errors.New("server: use -domain, -cert/-key ou -insecure")

// Run serve handler até ctx ser cancelado; então espera até 10 s pelas
// requisições em andamento. Conexões WebSocket já assumidas pelo handler não
// entram nessa espera.
func Run(ctx context.Context, opt Options, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{Addr: opt.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	var serve func() error
	switch {
	case opt.Insecure:
		logger.Info("HTTP sem TLS", "addr", opt.Addr)
		serve = srv.ListenAndServe
	case opt.Domain != "":
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(opt.Domain),
			Cache:      autocert.DirCache(opt.CertCacheDir),
		}
		srv.TLSConfig = m.TLSConfig()
		logger.Info("HTTPS automático", "domain", opt.Domain, "addr", opt.Addr)
		serve = func() error { return srv.ListenAndServeTLS("", "") }
	case opt.CertFile != "" && opt.KeyFile != "":
		logger.Info("HTTPS com certificado próprio", "addr", opt.Addr)
		serve = func() error { return srv.ListenAndServeTLS(opt.CertFile, opt.KeyFile) }
	default:
		return ErrNoTLSMode
	}

	errc := make(chan error, 1)
	go func() { errc <- serve() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		logger.Info("encerrando")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
