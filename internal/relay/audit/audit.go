// Package audit registra quem acessou qual máquina, quando e por quanto tempo.
//
// Regra deste contexto: nenhum acesso remoto acontece sem registro. Se o
// início do acesso não puder ser gravado, o relay recusa a conexão do técnico
// (veja [Log.Begin]).
package audit

import (
	"context"
	"log/slog"
	"time"
)

// Entry é um acesso de um técnico a uma máquina.
type Entry struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"userId"`   // 0 quando o usuário já foi removido
	Username   string     `json:"username"` // copiado: sobrevive à remoção do usuário
	AgentID    string     `json:"agentId"`
	Host       string     `json:"host"`
	RemoteAddr string     `json:"remoteAddr"`
	StartedAt  time.Time  `json:"startedAt"`
	EndedAt    *time.Time `json:"endedAt,omitempty"` // nil enquanto o acesso dura
}

// Store é a persistência dos registros de acesso, implementada em
// internal/relay/storage/postgres.
type Store interface {
	StartAccess(ctx context.Context, e Entry) (id int64, err error)
	EndAccess(ctx context.Context, id int64, at time.Time) error
	ListAccesses(ctx context.Context, limit int) ([]Entry, error)
	// CloseOpen grava at como fim de todo acesso ainda aberto e devolve
	// quantos eram.
	CloseOpen(ctx context.Context, at time.Time) (int64, error)
}

// Log aplica a regra de auditoria sobre um [Store].
type Log struct {
	store  Store
	logger *slog.Logger
	now    func() time.Time
}

// New cria o log de auditoria. Falhas ao gravar o fim de um acesso são
// escritas em logger.
func New(store Store, logger *slog.Logger) *Log {
	return &Log{store: store, logger: logger, now: time.Now}
}

// Begin grava o início de um acesso e devolve a função que grava o fim.
// Um erro significa que o acesso NÃO deve ser permitido.
//
// A função devolvida usa um contexto próprio porque normalmente é chamada
// quando a requisição do técnico já terminou.
func (l *Log) Begin(ctx context.Context, e Entry) (end func(), err error) {
	e.StartedAt = l.now()
	id, err := l.store.StartAccess(ctx, e)
	if err != nil {
		return nil, err
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := l.store.EndAccess(ctx, id, l.now()); err != nil {
			l.logger.Error("auditoria: falha ao gravar fim do acesso", "id", id, "err", err)
		}
	}, nil
}

// CloseOrphans encerra os acessos que ficaram abertos porque o relay parou
// no meio deles (um reinício derruba todas as conexões, e a função de fim
// devolvida por [Log.Begin] não chega a rodar). Deve ser chamada na partida,
// antes de aceitar conexões: nesse momento nenhum acesso está de fato aberto.
func (l *Log) CloseOrphans(ctx context.Context) (int64, error) {
	return l.store.CloseOpen(ctx, l.now())
}

// Recent devolve os últimos limit acessos, do mais novo para o mais antigo.
func (l *Log) Recent(ctx context.Context, limit int) ([]Entry, error) {
	return l.store.ListAccesses(ctx, limit)
}
