// Package mail entrega os e-mails de conta por SMTP. É infraestrutura:
// implementa [identity.Mailer] e não decide quem recebe o quê.
package mail

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"remoteit/internal/relay/identity"
)

//go:embed templates/message.html
var templates embed.FS

var layout = template.Must(template.ParseFS(templates, "templates/message.html"))

// retryDelays são as esperas antes de cada nova tentativa de envio.
var retryDelays = []time.Duration{2 * time.Second, 5 * time.Second}

const sendTimeout = 15 * time.Second

// Config descreve o servidor SMTP e o endereço público do console.
type Config struct {
	Host      string
	Port      int
	User      string
	Password  string
	StartTLS  bool // desligue só para um Mailpit local
	From      string
	FromName  string
	PublicURL string // base dos links, ex.: https://suporte.exemplo.com
}

// ConfigFromEnv lê SMTP_*, EMAIL_* e RELAY_PUBLIC_URL de getenv. enabled=false
// significa SMTP_HOST vazio: o relay funciona, sem cadastro nem recuperação.
func ConfigFromEnv(getenv func(string) string) (cfg Config, enabled bool, err error) {
	cfg = Config{
		Host:      getenv("SMTP_HOST"),
		Port:      587,
		User:      getenv("SMTP_USER"),
		Password:  getenv("SMTP_PASSWORD"),
		StartTLS:  getenv("SMTP_STARTTLS") != "false",
		From:      getenv("EMAIL_REMETENTE"),
		FromName:  getenv("EMAIL_REMETENTE_NOME"),
		PublicURL: strings.TrimRight(getenv("RELAY_PUBLIC_URL"), "/"),
	}
	if cfg.Host == "" {
		return cfg, false, nil
	}
	if p := getenv("SMTP_PORT"); p != "" {
		if cfg.Port, err = strconv.Atoi(p); err != nil || cfg.Port < 1 || cfg.Port > 65535 {
			return cfg, false, fmt.Errorf("SMTP_PORT inválida: %q", p)
		}
	}
	if cfg.FromName == "" {
		cfg.FromName = "RemoteIT"
	}
	u, err := url.Parse(cfg.PublicURL)
	switch {
	case err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return cfg, false, fmt.Errorf("defina RELAY_PUBLIC_URL como https://seu-dominio, sem caminho")
	case cfg.From == "":
		return cfg, false, fmt.Errorf("defina EMAIL_REMETENTE")
	case (cfg.User == "") != (cfg.Password == ""):
		return cfg, false, fmt.Errorf("defina SMTP_USER e SMTP_PASSWORD juntos")
	case cfg.User != "" && !cfg.StartTLS:
		return cfg, false, fmt.Errorf("SMTP com senha exige SMTP_STARTTLS=true")
	}
	return cfg, true, nil
}

// Sender envia por SMTP, uma conexão por mensagem.
type Sender struct {
	cfg  Config
	opts []gomail.Option
}

var _ identity.Mailer = (*Sender)(nil)

// New valida a configuração. A conexão só é aberta em cada envio.
func New(cfg Config) (*Sender, error) {
	policy := gomail.NoTLS
	if cfg.StartTLS {
		policy = gomail.TLSMandatory
	}
	opts := []gomail.Option{
		gomail.WithPort(cfg.Port), gomail.WithTLSPolicy(policy), gomail.WithTimeout(sendTimeout),
	}
	if cfg.User != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(cfg.User), gomail.WithPassword(cfg.Password))
	}
	if _, err := gomail.NewClient(cfg.Host, opts...); err != nil {
		return nil, fmt.Errorf("smtp: %w", err)
	}
	if err := gomail.NewMsg().FromFormat(cfg.FromName, cfg.From); err != nil {
		return nil, fmt.Errorf("EMAIL_REMETENTE: %w", err)
	}
	return &Sender{cfg: cfg, opts: opts}, nil
}

// Send entrega n, tentando de novo em falhas temporárias de rede ou servidor.
func (s *Sender) Send(ctx context.Context, n identity.Notice) error {
	msg, err := s.message(n)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		if err = s.deliver(ctx, msg); err == nil || attempt == len(retryDelays) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelays[attempt]):
		}
	}
}

func (s *Sender) deliver(ctx context.Context, msg *gomail.Msg) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	c, err := gomail.NewClient(s.cfg.Host, s.opts...)
	if err != nil {
		return err
	}
	return c.DialAndSendWithContext(ctx, msg)
}

type content struct {
	Subject, Text, Button, Link, Note string
}

func (s *Sender) content(n identity.Notice) (content, error) {
	// O token vai no fragmento (#), que o navegador não manda ao servidor:
	// fica fora dos logs do túnel e do Referer.
	link := func(route string) string {
		return s.cfg.PublicURL + "/#/" + route + "?token=" + url.QueryEscape(n.Token)
	}
	const ignore = "Se não foi você, ignore este e-mail."
	switch n.Kind {
	case identity.NoticeSignup:
		return content{"Crie sua conta no RemoteIT",
			"Recebemos um pedido de conta para este e-mail. Escolha seu nome de usuário e sua senha.",
			"Criar minha conta", link("criar-conta"), "O link vale por 24 horas. " + ignore}, nil
	case identity.NoticeReset:
		return content{"Redefina sua senha do RemoteIT",
			"Seu usuário é " + n.Username + ". Use o link para escolher uma nova senha.",
			"Escolher nova senha", link("nova-senha"), "O link vale por 1 hora. " + ignore}, nil
	case identity.NoticeLink:
		return content{"Confirme seu e-mail no RemoteIT",
			"O usuário " + n.Username + " pediu para usar este e-mail na recuperação da conta.",
			"Confirmar e-mail", link("confirmar-email"), "O link vale por 1 hora. " + ignore}, nil
	case identity.NoticePending:
		return content{"Conta aguardando aprovação no RemoteIT",
			n.Username + " (" + n.Email + ") criou uma conta. Aprove na aba Usuários só se conhecer a pessoa.",
			"Abrir o console", s.cfg.PublicURL + "/", "Uma conta aprovada acessa todas as máquinas."}, nil
	case identity.NoticeApproved:
		return content{"Sua conta do RemoteIT foi aprovada",
			"O usuário " + n.Username + " já pode entrar no console.",
			"Entrar", s.cfg.PublicURL + "/", ""}, nil
	}
	return content{}, fmt.Errorf("mail: aviso desconhecido %q", n.Kind)
}

func (s *Sender) message(n identity.Notice) (*gomail.Msg, error) {
	c, err := s.content(n)
	if err != nil {
		return nil, err
	}
	var html bytes.Buffer
	if err := layout.Execute(&html, c); err != nil {
		return nil, err
	}
	m := gomail.NewMsg()
	if err := m.FromFormat(s.cfg.FromName, s.cfg.From); err != nil {
		return nil, err
	}
	if err := m.To(n.To); err != nil {
		return nil, err
	}
	m.Subject(c.Subject)
	m.SetDate()
	m.SetMessageID()
	m.SetBodyString(gomail.TypeTextPlain, strings.TrimSpace(c.Text+"\n\n"+c.Link+"\n\n"+c.Note)+"\n")
	m.AddAlternativeString(gomail.TypeTextHTML, html.String())
	return m, nil
}
