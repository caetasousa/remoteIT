// Package update mantém o agente na versão que o relay anuncia
// (docs/adr/0011-atualizacao-automatica.md).
//
// Ao conectar e a cada hora, o agente pergunta ao relay qual é a versão da
// sua edição. Se for diferente da dele, confere a assinatura da oferta com a
// chave pública do projeto (embutida no build), espera não haver técnico
// conectado, baixa o .exe, confere o SHA-256 e entrega os bytes a Apply, que
// sabe se trocar no Windows (internal/agent/lifecycle).
//
// "Diferente", e não "maior": publicar no relay a versão anterior faz os
// agentes voltarem para ela. É o rollback.
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"remoteit/internal/agent/relayclient"
	"remoteit/internal/protocol"
)

const (
	firstCheck = 30 * time.Second // depois de iniciar, sem disputar a primeira conexão
	busyRetry  = time.Minute      // técnico conectado: tenta de novo logo
	maxExe     = 64 << 20
)

// ErrBusy: há uma atualização, mas um técnico está conectado.
var ErrBusy = errors.New("update: técnico conectado, atualização adiada")

// Updater confere e aplica atualizações. Os campos são obrigatórios, menos
// Interval (padrão 1 h).
type Updater struct {
	RelayURL  string // wss://dominio
	Edition   string // "portatil" ou "servico"
	Current   string // versão deste .exe
	PublicKey ed25519.PublicKey
	Creds     func(context.Context) (relayclient.Credentials, error)
	Busy      func() bool
	Apply     func(exe []byte) error
	Logger    *slog.Logger
	Interval  time.Duration
	Client    *http.Client

	once sync.Once
	kick chan struct{}
}

// kicks devolve o canal de "cheque já", criado uma vez só: Kick (goroutine
// do relayclient) e Run podem chegar aqui ao mesmo tempo.
func (u *Updater) kicks() chan struct{} {
	u.once.Do(func() { u.kick = make(chan struct{}, 1) })
	return u.kick
}

// Enabled informa se este build sabe se atualizar: precisa de versão e da
// chave pública (builds de desenvolvimento não têm).
func (u *Updater) Enabled() bool {
	return u.Current != "" && len(u.PublicKey) == ed25519.PublicKeySize
}

// Kick pede uma checagem já (o agente acabou de conectar ao relay, que pode
// ter sido atualizado agora).
func (u *Updater) Kick() {
	select {
	case u.kicks() <- struct{}{}:
	default:
	}
}

// Run checa até ctx acabar. Sem Enabled, não faz nada.
func (u *Updater) Run(ctx context.Context) {
	if !u.Enabled() {
		u.Logger.Info("atualização automática desligada neste build")
		return
	}
	interval := u.Interval
	if interval == 0 {
		interval = time.Hour
	}
	kick := u.kicks()
	t := time.NewTimer(firstCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
		next := interval
		switch err := u.Check(ctx); {
		case errors.Is(err, ErrBusy):
			next = busyRetry
		case err != nil:
			u.Logger.Info("checagem de atualização falhou", "err", err)
		}
		t.Reset(next)
	}
}

// Check faz uma checagem completa e, havendo versão nova, chama Apply.
func (u *Updater) Check(ctx context.Context) error {
	creds, err := u.Creds(ctx)
	if err != nil || creds.MachineID == "" {
		return err // agente legado não se atualiza
	}
	base, err := httpBase(u.RelayURL)
	if err != nil {
		return err
	}
	q := url.Values{"mid": {creds.MachineID}, "edition": {u.Edition}}.Encode()

	var offer protocol.UpdateOffer
	body, status, err := u.get(ctx, base+"/api/agent/update?"+q, creds.Secret, 1<<16)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent {
		return nil
	}
	if status != http.StatusOK {
		return fmt.Errorf("update: oferta respondeu HTTP %d", status)
	}
	if err := json.Unmarshal(body, &offer); err != nil {
		return err
	}
	if offer.Version == "" || offer.Version == u.Current {
		return nil
	}
	if err := protocol.VerifyUpdate(u.PublicKey, u.Edition, offer); err != nil {
		u.Logger.Error("atualização recusada: assinatura não confere", "versao", offer.Version)
		return err
	}
	if u.Busy() {
		return ErrBusy
	}

	exe, status, err := u.get(ctx, base+"/api/agent/update/"+u.Edition+"?"+url.Values{"mid": {creds.MachineID}}.Encode(), creds.Secret, maxExe)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("update: download respondeu HTTP %d", status)
	}
	sum := sha256.Sum256(exe)
	if hex.EncodeToString(sum[:]) != offer.SHA256 {
		u.Logger.Error("atualização recusada: o .exe baixado não é o assinado", "versao", offer.Version)
		return errors.New("update: SHA-256 do .exe não confere")
	}
	if u.Busy() { // um técnico pode ter entrado durante o download
		return ErrBusy
	}
	u.Logger.Info("atualizando o agente", "de", u.Current, "para", offer.Version)
	return u.Apply(exe)
}

func (u *Updater) get(ctx context.Context, url, secret string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	c := u.Client
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}}
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("update: resposta grande demais")
	}
	return b, resp.StatusCode, err
}

func httpBase(relayURL string) (string, error) {
	u, err := url.Parse(relayURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	}
	u.Path, u.RawQuery = "", ""
	return u.String(), nil
}
