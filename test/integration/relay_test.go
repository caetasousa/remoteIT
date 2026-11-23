//go:build integration

package integration

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/httpapi"
	"remoteit/internal/relay/hub"
	"remoteit/internal/relay/identity"
)

func TestLoginAndLogout(t *testing.T) {
	e := newEnv(t)
	anon := e.anonymous()
	if s := anon.do("GET", "/api/me", nil, nil); s != http.StatusUnauthorized {
		t.Errorf("sem login: status %d, want 401", s)
	}
	if s := anon.do("POST", "/api/login", map[string]string{"username": adminUser, "password": "errada"}, nil); s != http.StatusUnauthorized {
		t.Errorf("senha errada: status %d, want 401", s)
	}
	if s := anon.do("POST", "/api/login", map[string]string{"username": "fantasma", "password": "qualquer1"}, nil); s != http.StatusUnauthorized {
		t.Errorf("usuário inexistente: status %d, want 401", s)
	}

	c := e.login(adminUser, adminPass)
	var me identity.User
	if s := c.do("GET", "/api/me", nil, &me); s != 200 || me.Username != adminUser || me.Role != identity.RoleAdmin {
		t.Fatalf("me = %+v (status %d)", me, s)
	}
	if s := c.do("POST", "/api/logout", nil, nil); s != http.StatusNoContent {
		t.Errorf("logout: status %d", s)
	}
	if s := c.do("GET", "/api/me", nil, nil); s != http.StatusUnauthorized {
		t.Errorf("depois do logout: status %d, want 401", s)
	}
}

func TestLoginRequiresJSON(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Post(e.srv.URL+"/api/login", "application/x-www-form-urlencoded",
		bytes.NewBufferString("username=admin&password="+adminPass))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("formulário: status %d, want 415", resp.StatusCode)
	}
}

func TestAgentKeyRequired(t *testing.T) {
	e := newEnv(t)
	if _, err := e.rawAgent("pc1", "chave-errada-000000000"); err == nil {
		t.Fatal("agente com chave errada foi aceito")
	}
}

func TestAgentAppearsAndLeaves(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	e.waitAgent("pc1")

	ag.Close()
	admin := e.login(adminUser, adminPass)
	eventually(t, func() bool {
		var list []hub.Info
		admin.do("GET", "/api/agents", nil, &list)
		return len(list) == 0
	}, "agente desconectado continua na lista")
}

func TestViewerBridgesAgent(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	e.waitAgent("pc1")

	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	readText(t, ag, "start", 2*time.Second)

	frame := bytes.Repeat([]byte{0xAB}, 300_000) // maior que os buffers: testa o repasse em streaming
	if err := ag.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatal(err)
	}
	v.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, got, err := v.ReadMessage()
	if err != nil || mt != websocket.BinaryMessage || !bytes.Equal(got, frame) {
		t.Fatalf("técnico recebeu mt=%d len=%d err=%v; want quadro idêntico", mt, len(got), err)
	}

	if err := v.WriteMessage(websocket.TextMessage, []byte(`{"t":"kf"}`)); err != nil {
		t.Fatal(err)
	}
	readText(t, ag, "kf", 2*time.Second)

	v.Close()
	readText(t, ag, "stop", 2*time.Second)
}

func TestSecondViewerTakesOver(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	e.waitAgent("pc1")
	admin := e.login(adminUser, adminPass)

	v1, _, err := admin.viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	readText(t, ag, "start", 2*time.Second)
	v2, _, err := admin.viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	readText(t, ag, "start", 2*time.Second)

	v1.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := v1.ReadMessage(); err == nil {
		t.Error("primeiro técnico deveria ter sido desconectado")
	}
	var list []hub.Info
	admin.do("GET", "/api/agents", nil, &list)
	if !list[0].Busy || list[0].ViewedBy != adminUser {
		t.Errorf("info = %+v", list[0])
	}
}

func TestAgentLeavingClosesViewer(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	e.waitAgent("pc1")
	v, _, err := e.login(adminUser, adminPass).viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	readText(t, ag, "start", 2*time.Second)
	ag.Close()
	v.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := v.ReadMessage(); err == nil {
		t.Error("técnico deveria ser desconectado quando o agente sai")
	}
}

func TestViewerAccessControl(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	e.waitAgent("pc1")

	if _, resp, err := e.anonymous().viewer("pc1"); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("sem login: err=%v status=%v, want 401", err, resp)
	}
	admin := e.login(adminUser, adminPass)
	if _, resp, err := admin.viewerWithOrigin("pc1", "https://site-malicioso.example"); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("outra origem: err=%v, want 403 (proteção contra CSWSH)", err)
	}
	if _, resp, err := admin.viewer("nao-existe"); err == nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("agente offline: err=%v, want 404", err)
	}
}

func TestUserManagement(t *testing.T) {
	e := newEnv(t)
	admin := e.login(adminUser, adminPass)

	var tec identity.User
	if s := admin.do("POST", "/api/users", map[string]string{"username": "joao", "password": "senha-joao", "role": "technician"}, &tec); s != http.StatusCreated {
		t.Fatalf("cadastrar: status %d", s)
	}
	if s := admin.do("POST", "/api/users", map[string]string{"username": "joao", "password": "outra-senha", "role": "technician"}, nil); s != http.StatusConflict {
		t.Errorf("duplicado: status %d, want 409", s)
	}
	if s := admin.do("POST", "/api/users", map[string]string{"username": "x", "password": "curta", "role": "technician"}, nil); s != http.StatusBadRequest {
		t.Errorf("inválido: status %d, want 400", s)
	}

	joao := e.login("joao", "senha-joao")
	for _, path := range []string{"/api/users", "/api/audit"} {
		if s := joao.do("GET", path, nil, nil); s != http.StatusForbidden {
			t.Errorf("técnico em %s: status %d, want 403", path, s)
		}
	}
	if s := joao.do("GET", "/api/agents", nil, nil); s != 200 {
		t.Errorf("técnico em /api/agents: status %d", s)
	}

	var me identity.User
	admin.do("GET", "/api/me", nil, &me)
	if s := admin.do("DELETE", "/api/users/"+strconv.FormatInt(me.ID, 10), nil, nil); s != http.StatusBadRequest {
		t.Errorf("remover a si mesmo: status %d, want 400", s)
	}
	if s := admin.do("DELETE", "/api/users/"+strconv.FormatInt(tec.ID, 10), nil, nil); s != http.StatusNoContent {
		t.Fatalf("remover técnico: status %d", s)
	}
	if s := joao.do("GET", "/api/agents", nil, nil); s != http.StatusUnauthorized {
		t.Errorf("sessão de usuário removido: status %d, want 401", s)
	}
}

func TestAuditRecordsAccess(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	e.waitAgent("pc1")
	admin := e.login(adminUser, adminPass)

	v, _, err := admin.viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	readText(t, ag, "start", 2*time.Second)
	var entries []audit.Entry
	admin.do("GET", "/api/audit", nil, &entries)
	if len(entries) != 1 || entries[0].Username != adminUser || entries[0].Host != "PC1" || entries[0].EndedAt != nil {
		t.Fatalf("durante o acesso: %+v", entries)
	}

	v.Close()
	eventually(t, func() bool {
		admin.do("GET", "/api/audit", nil, &entries)
		return len(entries) == 1 && entries[0].EndedAt != nil
	}, "fim do acesso não foi registrado: %+v", entries)
}

// Apagar as sessões não basta: o WebSocket aberto já passou pela autenticação.
func TestRemovedUserLosesOpenViewer(t *testing.T) {
	e := newEnv(t)
	admin := e.login(adminUser, adminPass)
	var u identity.User
	if s := admin.do("POST", "/api/users", map[string]string{"username": "saindo", "password": "senha-saindo", "role": "technician"}, &u); s != http.StatusCreated {
		t.Fatalf("cadastrar: status %d", s)
	}
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	e.waitAgent("pc1")
	v, _, err := e.login("saindo", "senha-saindo").viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	readText(t, ag, "start", 2*time.Second)

	if s := admin.do("DELETE", "/api/users/"+strconv.FormatInt(u.ID, 10), nil, nil); s != http.StatusNoContent {
		t.Fatalf("remover: status %d", s)
	}
	readText(t, ag, "stop", 2*time.Second)
}

// Regra do contexto de auditoria: sem registro, sem acesso.
func TestAuditUnavailableRefusesViewer(t *testing.T) {
	e := newEnv(t)
	ag, err := e.rawAgent("pc1", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	e.waitAgent("pc1")
	admin := e.login(adminUser, adminPass)

	execSQL(t, `ALTER TABLE access_log RENAME TO access_log_off`)
	t.Cleanup(func() { execSQL(t, `ALTER TABLE access_log_off RENAME TO access_log`) })

	v, _, err := admin.viewer("pc1")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	v.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = v.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseInternalServerErr) {
		t.Errorf("err = %v, want close 1011 (auditoria indisponível)", err)
	}
	ag.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, msg, err := ag.ReadMessage(); err == nil {
		t.Errorf("agente recebeu %s sem acesso auditado", msg)
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	if s := e.anonymous().do("GET", "/healthz", nil, nil); s != 200 {
		t.Errorf("healthz: status %d", s)
	}
}

// Atrás do Cloudflare Tunnel toda conexão vem de 127.0.0.1; o IP real vem
// no cabeçalho, que só é aceito quando configurado.
func TestAuditClientIPHeader(t *testing.T) {
	cases := []struct {
		name, header, want string
	}{
		{"cabeçalho configurado", "Cf-Connecting-IP", "203.0.113.7"},
		{"sem configuração o cabeçalho é ignorado", "", "127.0.0.1:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, func(cfg *httpapi.Config) { cfg.ClientIPHeader = c.header })
			ag, err := e.rawAgent("pc1", agentKey)
			if err != nil {
				t.Fatal(err)
			}
			defer ag.Close()
			e.waitAgent("pc1")
			admin := e.login(adminUser, adminPass)
			v, _, err := admin.viewerWithHeader("pc1", http.Header{"Cf-Connecting-IP": {"203.0.113.7"}})
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close()
			readText(t, ag, "start", 2*time.Second)
			var entries []audit.Entry
			admin.do("GET", "/api/audit", nil, &entries)
			if len(entries) != 1 || !strings.HasPrefix(entries[0].RemoteAddr, c.want) {
				t.Errorf("remoteAddr = %+v, want prefixo %q", entries, c.want)
			}
		})
	}
}

// O download do agente não pode ser guardado em cache (Cloudflare guarda
// .exe por padrão): depois de um deploy, o próximo download tem de trazer o
// arquivo novo, e só para quem está logado.
func TestAgentDownloadIsNeverCached(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "suporte-ti.exe")
	if err := os.WriteFile(exe, []byte("versao-antiga"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, func(c *httpapi.Config) { c.AgentExe = exe })

	if s := e.anonymous().do("GET", "/download/agent.exe", nil, nil); s != http.StatusUnauthorized {
		t.Fatalf("sem login: status %d, want 401", s)
	}

	c := e.login(adminUser, adminPass)
	get := func() (string, http.Header) {
		t.Helper()
		resp, err := c.http.Get(e.srv.URL + "/download/agent.exe?t=1")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("download: status %d", resp.StatusCode)
		}
		return string(b), resp.Header
	}

	body, h := get()
	if body != "versao-antiga" {
		t.Fatalf("corpo = %q", body)
	}
	if cc := h.Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control = %q, want no-store e private", cc)
	}

	// como no deploy: o arquivo é substituído com o relay no ar
	if err := os.WriteFile(exe, []byte("versao-nova"), 0o644); err != nil {
		t.Fatal(err)
	}
	if body, _ := get(); body != "versao-nova" {
		t.Errorf("depois de trocar o arquivo veio %q, want versao-nova", body)
	}
}

// As duas edições do agente saem cada uma pela sua rota, com o nome certo.
func TestBothAgentEditionsDownload(t *testing.T) {
	dir := t.TempDir()
	portable, service := filepath.Join(dir, "suporte-ti.exe"), filepath.Join(dir, "suporte-ti-servico.exe")
	os.WriteFile(portable, []byte("portatil"), 0o644)
	os.WriteFile(service, []byte("servico"), 0o644)
	e := newEnv(t, func(c *httpapi.Config) { c.AgentExe, c.AgentServiceExe = portable, service })

	if s := e.anonymous().do("GET", "/download/agent-servico.exe", nil, nil); s != http.StatusUnauthorized {
		t.Fatalf("sem login: status %d, want 401", s)
	}
	c := e.login(adminUser, adminPass)
	for path, want := range map[string][2]string{
		"/download/agent.exe":         {"portatil", "suporte-ti.exe"},
		"/download/agent-servico.exe": {"servico", "suporte-ti-servico.exe"},
	} {
		resp, err := c.http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != want[0] || !strings.Contains(resp.Header.Get("Content-Disposition"), want[1]) {
			t.Errorf("%s: corpo %q, disposition %q", path, b, resp.Header.Get("Content-Disposition"))
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
			t.Errorf("%s: sem no-store", path)
		}
	}
}
