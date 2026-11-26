// Package update mantém o agente (e o visualizador do técnico) na versão que
// o relay anuncia
// (docs/DECISOES.md, ADR 0011).
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

	// Offer e Download, se definidos, trocam a origem da atualização (o
	// visualizador do técnico usa a sessão do usuário em vez da credencial
	// de máquina). Sem eles, valem RelayURL + Creds. A verificação de
	// assinatura e de hash é a mesma nos dois casos.
	Offer    func(ctx context.Context) (offer protocol.UpdateOffer, available bool, err error)
	Download func(ctx context.Context) ([]byte, error)

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
	offer, available, err := u.offer(ctx)
	if err != nil || !available {
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
	exe, err := u.download(ctx)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(exe)
	if hex.EncodeToString(sum[:]) != offer.SHA256 {
		u.Logger.Error("atualização recusada: o .exe baixado não é o assinado", "versao", offer.Version)
		return errors.New("update: SHA-256 do .exe não confere")
	}
	if u.Busy() { // um técnico pode ter entrado durante o download
		return ErrBusy
	}
	u.Logger.Info("atualizando", "de", u.Current, "para", offer.Version)
	return u.Apply(exe)
}

func (u *Updater) offer(ctx context.Context) (protocol.UpdateOffer, bool, error) {
	if u.Offer != nil {
		return u.Offer(ctx)
	}
	var offer protocol.UpdateOffer
	creds, err := u.Creds(ctx)
	if err != nil || creds.MachineID == "" {
		return offer, false, err // agente legado não se atualiza
	}
	base, err := httpBase(u.RelayURL)
	if err != nil {
		return offer, false, err
	}
	q := url.Values{"mid": {creds.MachineID}, "edition": {u.Edition}}.Encode()
	body, status, err := u.get(ctx, base+"/api/agent/update?"+q, creds.Secret, 1<<16)
	switch {
	case err != nil:
		return offer, false, err
	case status == http.StatusNoContent:
		return offer, false, nil
	case status != http.StatusOK:
		return offer, false, fmt.Errorf("update: oferta respondeu HTTP %d", status)
	}
	return offer, true, json.Unmarshal(body, &offer)
}

func (u *Updater) download(ctx context.Context) ([]byte, error) {
	if u.Download != nil {
		return u.Download(ctx)
	}
	creds, err := u.Creds(ctx)
	if err != nil {
		return nil, err
	}
	base, err := httpBase(u.RelayURL)
	if err != nil {
		return nil, err
	}
	exe, status, err := u.get(ctx, base+"/api/agent/update/"+u.Edition+"?"+url.Values{"mid": {creds.MachineID}}.Encode(), creds.Secret, maxExe)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("update: download respondeu HTTP %d", status)
	}
	return exe, err
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
