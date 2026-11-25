//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"remoteit/internal/agent/relayclient"
	"remoteit/internal/agent/update"
	"remoteit/internal/protocol"
	"remoteit/internal/relay/httpapi"
)

// signedDist monta uma pasta dist/ como o build.sh: os dois .exe e o
// update.json assinado pela ferramenta de verdade (cmd/signupdate). Devolve
// a pasta e a chave pública que o agente teria embutida.
func signedDist(t *testing.T, version string) (dir string, pub ed25519.PublicKey) {
	t.Helper()
	dir = t.TempDir()
	key := filepath.Join(t.TempDir(), "update.key")
	os.WriteFile(filepath.Join(dir, "suporte-ti.exe"), fakeExe("portatil-"+version), 0o644)
	os.WriteFile(filepath.Join(dir, "suporte-ti-servico.exe"), fakeExe("servico-"+version), 0o644)
	sign := func(args ...string) string {
		out, err := exec.Command("go", append([]string{"run", "remoteit/cmd/signupdate", "-key", key}, args...)...).Output()
		if err != nil {
			t.Fatalf("signupdate %v: %v", args, err)
		}
		return string(out)
	}
	sign("-genkey")
	b, _ := base64.StdEncoding.DecodeString(sign("-pub"))
	sign("-sign", dir, "-version", version)
	return dir, ed25519.PublicKey(b)
}

func withDist(dir string) func(*httpapi.Config) {
	return func(c *httpapi.Config) {
		c.AgentExe = filepath.Join(dir, "suporte-ti.exe")
		c.AgentServiceExe = filepath.Join(dir, "suporte-ti-servico.exe")
		c.UpdateManifest = filepath.Join(dir, "update.json")
	}
}

// updater monta o Updater de uma máquina cadastrada, com Apply que só guarda
// o .exe recebido.
func (e *env) updater(pub ed25519.PublicKey, current string, creds relayclient.Credentials) (*update.Updater, *[]byte) {
	var applied []byte
	return &update.Updater{
		RelayURL: e.wsURL(""), Edition: "servico", Current: current, PublicKey: pub,
		Creds:  func(context.Context) (relayclient.Credentials, error) { return creds, nil },
		Busy:   func() bool { return false },
		Apply:  func(exe []byte) error { applied = exe; return nil },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}, &applied
}

// A atualização automática de ponta a ponta: o relay anuncia a versão do
// update.json, o agente confere a assinatura e o hash e recebe exatamente o
// .exe publicado; na mesma versão, nada acontece.
func TestAgentUpdate(t *testing.T) {
	dir, pub := signedDist(t, "2026.11.25.0300")
	e := newEnv(t, withDist(dir))
	ana := e.newTech("ana")
	id, secret, _ := e.enroll(ana.downloadCode("/download/agent-servico.exe"), "PC1")
	creds := relayclient.Credentials{MachineID: id, Secret: secret}

	u, applied := e.updater(pub, "2026.11.24.2200", creds)
	if err := u.Check(t.Context()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	want, _ := os.ReadFile(filepath.Join(dir, "suporte-ti-servico.exe"))
	if string(*applied) != string(want) {
		t.Fatalf("o agente não recebeu o .exe publicado (%d bytes)", len(*applied))
	}
	if !strings.Contains(string(*applied), protocol.EnrollMarker+protocol.EnrollEmpty) {
		t.Error("o .exe da atualização deveria vir sem código gravado (a máquina já tem credencial)")
	}

	same, applied2 := e.updater(pub, "2026.11.25.0300", creds)
	if err := same.Check(t.Context()); err != nil || *applied2 != nil {
		t.Errorf("na mesma versão não deveria atualizar (err %v)", err)
	}

	busy, applied3 := e.updater(pub, "velha", creds)
	busy.Busy = func() bool { return true }
	if err := busy.Check(t.Context()); !errors.Is(err, update.ErrBusy) || *applied3 != nil {
		t.Errorf("com técnico conectado: err = %v, want ErrBusy e nada aplicado", err)
	}

	// legado (sem credencial de máquina) não se atualiza
	legacy, applied4 := e.updater(pub, "velha", relayclient.Credentials{LegacyID: "pc1", LegacyKey: agentKey})
	if err := legacy.Check(t.Context()); err != nil || *applied4 != nil {
		t.Errorf("agente legado não deveria atualizar (err %v)", err)
	}

	// a oferta e o .exe exigem a credencial da máquina
	for _, path := range []string{"/api/agent/update?edition=servico&mid=" + id, "/api/agent/update/servico?mid=" + id} {
		if r, _ := http.Get(e.srv.URL + path); r.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s sem credencial: status %d", path, r.StatusCode)
		}
	}
}

// Um relay invadido (ou um update.json trocado) não consegue empurrar outro
// .exe: sem a chave privada do projeto, a assinatura não confere.
func TestAgentUpdateRejectsTampering(t *testing.T) {
	dir, pub := signedDist(t, "2026.11.25.0300")
	e := newEnv(t, withDist(dir))
	ana := e.newTech("ana")
	id, secret, _ := e.enroll(ana.downloadCode("/download/agent-servico.exe"), "PC1")
	creds := relayclient.Credentials{MachineID: id, Secret: secret}

	// 1) .exe trocado no servidor, manifesto original: o hash não confere
	os.WriteFile(filepath.Join(dir, "suporte-ti-servico.exe"), fakeExe("malicioso"), 0o644)
	u, applied := e.updater(pub, "velha", creds)
	if err := u.Check(t.Context()); err == nil || *applied != nil {
		t.Errorf(".exe trocado foi aplicado (err %v)", err)
	}

	// 2) manifesto reescrito com o hash do .exe malicioso: a assinatura não confere
	var m protocol.UpdateManifest
	b, _ := os.ReadFile(filepath.Join(dir, "update.json"))
	json.Unmarshal(b, &m)
	f := m.Files["servico"]
	f.SHA256 = strings.Repeat("0", 64)
	m.Files["servico"] = f
	b, _ = json.Marshal(m)
	os.WriteFile(filepath.Join(dir, "update.json"), b, 0o644)
	if err := u.Check(t.Context()); !errors.Is(err, protocol.ErrBadSignature) || *applied != nil {
		t.Errorf("manifesto adulterado: err = %v, want ErrBadSignature", err)
	}

	// 3) agente com outra chave pública (outro projeto): recusa
	other, _, _ := ed25519.GenerateKey(nil)
	dir2, _ := signedDist(t, "2026.11.25.0400")
	e2 := newEnv(t, withDist(dir2))
	bia := e2.newTech("bia")
	id2, secret2, _ := e2.enroll(bia.downloadCode("/download/agent-servico.exe"), "PC2")
	u2, applied2 := e2.updater(other, "velha", relayclient.Credentials{MachineID: id2, Secret: secret2})
	if err := u2.Check(t.Context()); !errors.Is(err, protocol.ErrBadSignature) || *applied2 != nil {
		t.Errorf("chave de outro projeto: err = %v, want ErrBadSignature", err)
	}
}

// A versão que o agente informa ao conectar aparece no console.
func TestAgentVersionListed(t *testing.T) {
	e := newEnv(t)
	ana := e.newTech("ana")
	id, _ := ana.ownMachine()
	if m, _ := ana.machine(id); m.Version != "" {
		t.Errorf("agente sem versão deveria vir vazio, veio %q", m.Version)
	}
	var link struct{ Path string }
	ana.do("POST", "/api/links", map[string]any{"edition": "portatil", "hours": 1, "uses": 1}, &link)
	id2, secret, _ := e.enroll(strings.TrimPrefix(link.Path, "/i/"), "PC2")
	h := http.Header{"Authorization": {"Bearer " + secret}}
	ag, _, err := websocket.DefaultDialer.Dial(e.wsURL("/ws/agent?mid="+id2+"&host=PC2&user=ana&ver=2026.11.25.0300"), h)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	eventually(t, func() bool { m, _ := ana.machine(id2); return m.Version == "2026.11.25.0300" }, "versão não apareceu na lista")
}
