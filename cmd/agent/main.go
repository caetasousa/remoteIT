// Command agent é o agente do RemoteIT: um único .exe, sem instalação e sem
// admin. Conecta PARA FORA no relay (porta 443), pede consentimento ao
// usuário e transmite a tela.
//
// Uso:
//
//	suporte-ti.exe [-relay wss://dominio] [-key chave] [-noconsent]
//	suporte-ti.exe install | uninstall       (modo serviço, pede o UAC)
//
// O build gera duas edições do mesmo código (main.edition): a portátil
// (suporte-ti.exe), que só executa, e a de serviço (suporte-ti-servico.exe),
// em que os dois cliques equivalem a "install".
//
// REMOTEIT_RELAY e REMOTEIT_KEY no ambiente têm prioridade sobre as flags e
// sobre os valores embutidos no build.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"remoteit/internal/agent/consent"
	"remoteit/internal/agent/desktop"
	"remoteit/internal/agent/input"
	"remoteit/internal/agent/lifecycle"
	"remoteit/internal/agent/lock"
	"remoteit/internal/agent/media"
	"remoteit/internal/agent/media/capture"
	"remoteit/internal/agent/media/h264"
	"remoteit/internal/agent/media/jpeg"
	"remoteit/internal/agent/relayclient"
	"remoteit/internal/agent/session"
)

// Embutidos pelo build.sh: -ldflags "-X main.relayURL=wss://host -X main.agentKey=..."
var (
	relayURL = "wss://suporte.exemplo.com"
	agentKey = ""
	// "1" desliga o consentimento sem precisar de -noconsent na linha de comando.
	defaultNoConsent = ""
	// "servico" gera a edição que instala o serviço com dois cliques.
	edition = ""
)

func main() {
	mode := lifecycle.Dispatch(os.Args, edition == "servico")
	if mode == lifecycle.Handled {
		return
	}
	flag.StringVar(&relayURL, "relay", relayURL, "URL do relay (wss://dominio)")
	flag.StringVar(&agentKey, "key", agentKey, "chave do agente")
	noConsent := flag.Bool("noconsent", defaultNoConsent == "1",
		"não pedir consentimento (servidores sem usuário; respeite a política da empresa)")
	if mode == lifecycle.Worker {
		flag.CommandLine.Parse(os.Args[2:])
	} else {
		flag.Parse()
	}
	if v := os.Getenv("REMOTEIT_RELAY"); v != "" {
		relayURL = v
	}
	if v := os.Getenv("REMOTEIT_KEY"); v != "" {
		agentKey = v
	}
	lifecycle.Init(mode)

	logger := slog.New(slog.NewTextHandler(lifecycle.LogOutput(mode), nil))
	slog.SetDefault(logger)
	logger.Info("agente iniciado", "modo", mode, "relay", relayURL)
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}

	ask := consent.Dialog
	if *noConsent {
		ask = consent.Always
	}
	encoders := media.Encoders{
		H264: h264.New, JPEG: jpeg.New,
		CursorShape:   capture.CursorShape,
		FollowDesktop: desktop.FollowInput,
	}
	injector := input.New(desktop.FollowInput)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	relayclient.Run(ctx,
		relayclient.Config{URL: relayURL, Key: agentKey, ID: strings.ToLower(host), Host: host, User: user},
		func(send media.Sender) relayclient.Handler {
			return session.New(session.Config{
				Host:    host,
				Consent: ask,
				Input:   injector,
				Lock:    lock.Station,
				NewStream: func(s media.Sender) session.Stream {
					return media.NewStream(encoders, s, logger)
				},
				Logger: logger,
			}, send)
		},
		logger)
}
