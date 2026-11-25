// Command agent é o agente do RemoteIT: um único .exe, sem instalação e sem
// admin. Conecta PARA FORA no relay (porta 443), pede consentimento ao
// usuário e transmite a tela.
//
// Uso:
//
//	suporte-ti.exe [-relay wss://dominio] [-code codigo] [-key chave] [-noconsent]
//	suporte-ti.exe install | uninstall       (modo serviço, pede o UAC)
//
// O build gera duas edições do mesmo código (main.edition): a portátil
// (suporte-ti.exe), que só executa, e a de serviço (suporte-ti-servico.exe),
// em que os dois cliques equivalem a "install".
//
// REMOTEIT_RELAY, REMOTEIT_CODE e REMOTEIT_KEY no ambiente têm prioridade
// sobre as flags e sobre os valores embutidos no build.
//
// Identidade: o .exe baixado pelo console traz gravado o código de cadastro
// do técnico (internal/agent/machine). Na primeira execução o agente troca o
// código por uma credencial desta máquina e passa a usá-la. -code passa o
// código à mão (testes e portátil; o worker do serviço usa o gravado no .exe).
// Sem código nem credencial, o agente cai para a chave compartilhada (modo
// legado).
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"

	"remoteit/internal/agent/clipboard"
	"remoteit/internal/agent/consent"
	"remoteit/internal/agent/desktop"
	"remoteit/internal/agent/input"
	"remoteit/internal/agent/lifecycle"
	"remoteit/internal/agent/lock"
	"remoteit/internal/agent/machine"
	"remoteit/internal/agent/media"
	"remoteit/internal/agent/media/capture"
	"remoteit/internal/agent/media/gpu"
	"remoteit/internal/agent/media/jpeg"
	"remoteit/internal/agent/relayclient"
	"remoteit/internal/agent/session"
	"remoteit/internal/agent/update"
)

// Embutidos pelo build.sh: -ldflags "-X main.relayURL=wss://host -X main.agentKey=..."
var (
	relayURL = "wss://suporte.exemplo.com"
	agentKey = ""
	// "1" desliga o consentimento sem precisar de -noconsent na linha de comando.
	defaultNoConsent = ""
	// "servico" gera a edição que instala o serviço com dois cliques.
	edition = ""
	// version e updatePublicKey (base64) ligam a atualização automática;
	// sem eles (build de desenvolvimento) o agente nunca se troca.
	version         = ""
	updatePublicKey = ""
)

func main() {
	mode := lifecycle.Dispatch(os.Args, edition == "servico")
	if mode == lifecycle.Handled {
		return
	}
	flag.StringVar(&relayURL, "relay", relayURL, "URL do relay (wss://dominio)")
	flag.StringVar(&agentKey, "key", agentKey, "chave compartilhada (modo legado)")
	code := flag.String("code", machine.EmbeddedCode(), "código de cadastro da máquina")
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
	if v := os.Getenv("REMOTEIT_CODE"); v != "" {
		*code = v
	}
	lifecycle.Init(mode)

	logger := slog.New(slog.NewTextHandler(lifecycle.LogOutput(mode), nil))
	slog.SetDefault(logger)
	logger.Info("agente iniciado", "modo", mode, "versao", version, "relay", relayURL)
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
		H264: gpu.NewH264, AVC444: gpu.NewAVC444, AV1: gpu.NewAV1, JPEG: jpeg.New,
		CursorShape:   capture.CursorShape,
		FollowDesktop: desktop.FollowInput,
		// refresh de nitidez: captura GDI sem perda quando a tela para
		Still: func() (media.Still, error) { return capture.New() },
	}
	injector := input.New(desktop.FollowInput)

	keeper := &machine.Keeper{
		RelayURL: relayURL, Code: *code, Name: host,
		Store: machine.FileStore{
			Path:         filepath.Join(lifecycle.StateDir(mode), "machine.json"),
			MachineScope: mode == lifecycle.Worker,
		},
		Enroll:   machine.Enroll,
		LegacyID: strings.ToLower(host), LegacyKey: agentKey,
		Logger: logger,
	}

	// atualização automática: a edição a buscar segue o modo em que roda
	var busy atomic.Bool
	updEdition := "portatil"
	if mode == lifecycle.Worker {
		updEdition = "servico"
	}
	pub, _ := base64.StdEncoding.DecodeString(updatePublicKey)
	updater := &update.Updater{
		RelayURL: relayURL, Edition: updEdition, Current: version, PublicKey: pub,
		Creds: keeper.Credentials, Busy: busy.Load,
		Apply:  func(exe []byte) error { return lifecycle.ApplyUpdate(mode, exe) },
		Logger: logger,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go updater.Run(ctx)
	relayclient.Run(ctx,
		relayclient.Config{URL: relayURL, Host: host, User: user, Version: version, Auth: keeper, OnConnect: updater.Kick},
		func(send media.Sender) relayclient.Handler {
			return session.New(session.Config{
				Host:      host,
				Consent:   ask,
				Input:     injector,
				Lock:      lock.Station,
				Clipboard: clipboard.Set,
				OnActive:  busy.Store,
				NewStream: func(s media.Sender) session.Stream {
					return media.NewStream(encoders, s, logger)
				},
				Logger: logger,
			}, send)
		},
		logger)
}
