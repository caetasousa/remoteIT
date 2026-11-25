package machine

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
)

type memStore struct {
	c  Credential
	ok bool
}

func (m *memStore) Load() (Credential, bool) { return m.c, m.ok }
func (m *memStore) Save(c Credential) error  { m.c, m.ok = c, true; return nil }
func (m *memStore) Clear() error             { m.ok = false; return nil }

func keeper(code string, store Store, enroll func(context.Context, string, string, string) (Credential, error)) *Keeper {
	return &Keeper{Code: code, Name: "PC1", Store: store, Enroll: enroll,
		LegacyID: "pc1", LegacyKey: "chave", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestEmbeddedCodeEmptyWhenNotStamped(t *testing.T) {
	if c := EmbeddedCode(); c != "" {
		t.Fatalf("build de teste não deveria ter código: %q", c)
	}
}

func TestKeeperEnrollsOnceAndReusesCredential(t *testing.T) {
	calls := 0
	enroll := func(context.Context, string, string, string) (Credential, error) {
		calls++
		return Credential{MachineID: "m_1", Secret: "s"}, nil
	}
	store := &memStore{}
	k := keeper("codigo-da-ana", store, enroll)
	for range 2 {
		c, err := k.Credentials(t.Context())
		if err != nil || c.MachineID != "m_1" {
			t.Fatalf("credenciais = %+v, %v", c, err)
		}
	}
	if calls != 1 {
		t.Errorf("cadastrou %d vezes, want 1", calls)
	}

	// .exe de outra técnica no mesmo PC: cadastra de novo, no nome dela
	k2 := keeper("codigo-da-bia", store, func(context.Context, string, string, string) (Credential, error) {
		return Credential{MachineID: "m_2", Secret: "s2"}, nil
	})
	if c, _ := k2.Credentials(t.Context()); c.MachineID != "m_2" {
		t.Errorf("código diferente deveria recadastrar, veio %+v", c)
	}
}

func TestKeeperFallbacks(t *testing.T) {
	rejected := func(context.Context, string, string, string) (Credential, error) {
		return Credential{}, ErrCodeRejected
	}

	// código recusado, sem credencial: modo legado
	c, err := keeper("vencido", &memStore{}, rejected).Credentials(t.Context())
	if err != nil || c.LegacyKey != "chave" || c.MachineID != "" {
		t.Errorf("sem credencial e com código recusado deveria cair no legado: %+v, %v", c, err)
	}

	// código recusado, com credencial antiga: mantém a credencial
	store := &memStore{c: Credential{MachineID: "m_1", Secret: "s", CodeHash: codeHash("outro")}, ok: true}
	if c, _ := keeper("vencido", store, rejected).Credentials(t.Context()); c.MachineID != "m_1" {
		t.Errorf("deveria manter a credencial antiga: %+v", c)
	}

	// erro de rede: não cai no legado, tenta de novo depois
	offline := func(context.Context, string, string, string) (Credential, error) {
		return Credential{}, errors.New("sem rede")
	}
	if _, err := keeper("codigo", &memStore{}, offline).Credentials(t.Context()); err == nil {
		t.Error("erro de rede deveria ser devolvido para o backoff")
	}

	// sem nada: erro
	k := keeper("", &memStore{}, rejected)
	k.LegacyKey = ""
	if _, err := k.Credentials(t.Context()); !errors.Is(err, ErrNoIdentity) {
		t.Errorf("err = %v, want ErrNoIdentity", err)
	}
}

func TestKeeperRejected(t *testing.T) {
	store := &memStore{c: Credential{MachineID: "m_1", Secret: "s"}, ok: true}
	k := keeper("", store, nil)
	k.Rejected(403) // removida: não apaga nada
	if !store.ok {
		t.Fatal("403 não deveria apagar a credencial")
	}
	k.Rejected(401) // credencial inválida: apaga para recadastrar
	if store.ok {
		t.Fatal("401 deveria apagar a credencial")
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	f := FileStore{Path: filepath.Join(t.TempDir(), "sub", "machine.json")}
	if _, ok := f.Load(); ok {
		t.Fatal("arquivo inexistente deveria dar false")
	}
	want := Credential{MachineID: "m_1", Secret: "segredo", CodeHash: "h"}
	if err := f.Save(want); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.Load(); !ok || got != want {
		t.Fatalf("Load = %+v, %v", got, ok)
	}
	f.Clear()
	if _, ok := f.Load(); ok {
		t.Fatal("Clear não apagou")
	}
}
