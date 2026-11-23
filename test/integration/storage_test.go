//go:build integration

package integration

import (
	"testing"
	"time"

	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/identity"
)

func TestMigrateIsIdempotent(t *testing.T) {
	e := newEnv(t)
	if err := e.db.Migrate(t.Context()); err != nil {
		t.Fatalf("segunda execução: %v", err)
	}
}

// O registro de auditoria sobrevive à remoção do usuário.
func TestAuditKeepsRemovedUser(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	ids := identity.NewService(e.db)
	u, err := ids.CreateUser(ctx, "maria", "senha-maria", identity.RoleTechnician)
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.db.StartAccess(ctx, audit.Entry{UserID: u.ID, Username: u.Username, AgentID: "pc1", Host: "PC1", RemoteAddr: "10.0.0.1:5000", StartedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.EndAccess(ctx, id, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.db.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	list, err := e.db.ListAccesses(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].UserID != 0 || list[0].Username != "maria" || list[0].EndedAt == nil {
		t.Errorf("registro = %+v", list)
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	ids := identity.NewService(e.db)
	token, _, err := ids.Login(ctx, adminUser, adminPass)
	if err != nil {
		t.Fatal(err)
	}
	execSQL(t, `UPDATE sessions SET expires_at = now() - interval '1 minute'`)
	if _, err := ids.Authenticate(ctx, token); err != identity.ErrInvalidCredentials {
		t.Errorf("sessão vencida: err = %v, want ErrInvalidCredentials", err)
	}
}

func TestEnsureAdminOnlyOnce(t *testing.T) {
	e := newEnv(t)
	created, err := identity.NewService(e.db).EnsureAdmin(t.Context(), "outro", "outra-senha")
	if err != nil || created {
		t.Errorf("com usuários existentes: created=%v err=%v, want false/nil", created, err)
	}
}
