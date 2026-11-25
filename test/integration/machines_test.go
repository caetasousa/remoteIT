//go:build integration

package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"remoteit/internal/protocol"
	"remoteit/internal/relay/audit"
	"remoteit/internal/relay/httpapi"
)

// newTech cadastra um técnico e devolve o cliente logado.
func (e *env) newTech(name string) *client {
	e.t.Helper()
	admin := e.login(adminUser, adminPass)
	if s := admin.do("POST", "/api/users", map[string]string{"username": name, "password": "senha-" + name, "role": "technician"}, nil); s != http.StatusCreated {
		e.t.Fatalf("cadastrar %s: status %d", name, s)
	}
	return e.login(name, "senha-"+name)
}

// newTechName cadastra um técnico e devolve o nome (senha "senha-"+nome).
func (e *env) newTechName(name string) string {
	e.newTech(name)
	return name
}

// downloadCode baixa o .exe pelo console e devolve o código gravado nele.
func (c *client) downloadCode(path string) string {
	c.e.t.Helper()
	resp, err := c.http.Get(c.e.srv.URL + path)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.e.t.Fatalf("baixar %s: status %d", path, resp.StatusCode)
	}
	return stampedCode(c.e.t, b)
}

func stampedCode(t *testing.T, exe []byte) string {
	t.Helper()
	i := bytes.Index(exe, []byte(protocol.EnrollMarker))
	if i < 0 {
		t.Fatal("o .exe baixado não tem o espaço do código")
	}
	slot := exe[i+len(protocol.EnrollMarker) : i+len(protocol.EnrollMarker)+protocol.EnrollCodeLen]
	code := strings.TrimRight(string(slot), string(protocol.EnrollPad))
	if code == "" {
		t.Fatal("o .exe baixado veio sem código")
	}
	return code
}

// enroll cadastra uma máquina direto pela API, como o agente faz.
func (e *env) enroll(code, name string) (id, secret string, status int) {
	e.t.Helper()
	var out struct{ ID, Secret string }
	status = e.anonymous().do("POST", "/api/agent/enroll", map[string]string{"code": code, "name": name}, &out)
	return out.ID, out.Secret, status
}

// machineAgent conecta um agente falso com a credencial da máquina.
func (e *env) machineAgent(id, secret string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{"Authorization": {"Bearer " + secret}}
	return websocket.DefaultDialer.Dial(e.wsURL("/ws/agent?mid="+id+"&host=PC-DA-ANA&user=ana"), h)
}

func withExe(t *testing.T) (portable string, opt func(*httpapi.Config)) {
	dir := t.TempDir()
	portable = filepath.Join(dir, "suporte-ti.exe")
	service := filepath.Join(dir, "suporte-ti-servico.exe")
	os.WriteFile(portable, fakeExe("portatil"), 0o644)
	os.WriteFile(service, fakeExe("servico"), 0o644)
	return portable, func(c *httpapi.Config) { c.AgentExe, c.AgentServiceExe = portable, service }
}

// O caminho completo do item "cada técnico com as suas máquinas": o técnico
// baixa o agente, o binário de verdade roda, se cadastra sozinho com o
// código gravado no download e aparece só para o dono e os administradores.
// Rodar de novo reusa a mesma identidade.
func TestDownloadedAgentBelongsToItsTechnician(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "agent")
	build := exec.Command("go", "build", "-o", bin, "remoteit/cmd/agent")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compilar o agente: %v\n%s", err, out)
	}
	e := newEnv(t, func(c *httpapi.Config) { c.AgentExe = bin })
	ana, bia := e.newTech("ana"), e.newTech("bia")

	resp, err := ana.http.Get(e.srv.URL + "/download/agent.exe")
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	stamped := filepath.Join(t.TempDir(), "suporte-ti")
	os.WriteFile(stamped, exe, 0o755)

	state := t.TempDir()
	run := func() (stop func()) {
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, stamped, "-noconsent")
		var logs bytes.Buffer
		cmd.Stderr = &logs
		cmd.Env = append(os.Environ(), "REMOTEIT_RELAY="+e.wsURL(""), "REMOTEIT_STATE_DIR="+state, "REMOTEIT_KEY=")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return func() {
			cancel()
			cmd.Wait()
			if t.Failed() {
				t.Logf("log do agente:\n%s", logs.String())
			}
		}
	}

	stop := run()
	var id string
	eventually(t, func() bool {
		for _, m := range ana.machines() {
			if m.Online && m.Mine && !m.Legacy {
				id = m.ID
				return true
			}
		}
		return false
	}, "a máquina do .exe da ana não apareceu para ela")
	if !strings.HasPrefix(id, "m_") {
		t.Errorf("ID da máquina = %q, want aleatório com prefixo m_", id)
	}
	if list := bia.machines(); len(list) != 0 {
		t.Fatalf("bia vê máquinas que não são dela: %+v", list)
	}
	if _, resp, err := bia.viewer(id); err == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bia abriu a tela da máquina da ana")
	}
	if m, ok := e.login(adminUser, adminPass).machine(id); !ok || m.Owner != "ana" {
		t.Errorf("admin deveria ver a máquina com dona ana: %+v", m)
	}
	v, _, err := ana.viewer(id)
	if err != nil {
		t.Fatalf("a dona não abriu a tela: %v", err)
	}
	v.Close()

	// segunda execução: mesma credencial, nenhuma máquina nova
	stop()
	stop = run()
	defer stop()
	eventually(t, func() bool { m, _ := ana.machine(id); return m.Online }, "o agente não voltou com a mesma identidade")
	if n := len(ana.machines()); n != 1 {
		t.Errorf("a segunda execução criou outra máquina: %d máquinas", n)
	}
}

// A credencial é da máquina: outra não se passa por ela, nem pelo ID, nem
// pelo nome do computador, e remover a máquina a desconecta de vez.
func TestMachineCredentialRules(t *testing.T) {
	_, opt := withExe(t)
	e := newEnv(t, opt)
	ana := e.newTech("ana")
	id, secret, s := e.enroll(ana.downloadCode("/download/agent.exe"), "PC-DA-ANA")
	if s != http.StatusCreated {
		t.Fatalf("cadastro: status %d", s)
	}

	if _, resp, err := e.machineAgent(id, "credencial-errada"); err == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("credencial errada deveria dar 401")
	}
	// um agente legado não pode usar o ID de uma máquina cadastrada
	if _, err := e.rawAgent(id, agentKey); err == nil {
		t.Fatal("agente legado conectou com o ID de uma máquina cadastrada")
	}
	// mesmo nome de computador por um legado: outra máquina, a da ana segue dela
	leg, err := e.rawAgent("pc-da-ana", agentKey)
	if err != nil {
		t.Fatal(err)
	}
	defer leg.Close()

	ag, _, err := e.machineAgent(id, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	eventually(t, func() bool { m, _ := ana.machine(id); return m.Online }, "máquina não ficou online")
	if list := ana.machines(); len(list) != 1 {
		t.Errorf("ana deveria ver só a máquina dela, viu %+v", list)
	}

	if s := e.newTech("bia").do("DELETE", "/api/machines/"+id, nil, nil); s != http.StatusForbidden && s != http.StatusNotFound {
		t.Errorf("outra técnica removeu a máquina: status %d", s)
	}
	if s := ana.do("DELETE", "/api/machines/"+id, nil, nil); s != http.StatusNoContent {
		t.Fatalf("remover: status %d", s)
	}
	ag.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := ag.ReadMessage(); err == nil {
		t.Error("máquina removida continuou conectada")
	}
	if _, resp, err := e.machineAgent(id, secret); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Error("máquina removida deveria receber 403 ao reconectar")
	}
	if _, ok := ana.machine(id); ok {
		t.Error("máquina removida continua na lista")
	}
}

// Link de instalação: público, com limite de máquinas, revogável e só do
// técnico que o criou.
func TestInstallLink(t *testing.T) {
	_, opt := withExe(t)
	e := newEnv(t, opt)
	ana := e.newTech("ana")

	var link struct {
		ID      int64  `json:"id"`
		Path    string `json:"path"`
		Edition string `json:"edition"`
	}
	if s := ana.do("POST", "/api/links", map[string]any{"edition": "servico", "hours": 24, "uses": 1}, &link); s != http.StatusCreated {
		t.Fatalf("criar link: status %d", s)
	}
	if s := ana.do("POST", "/api/links", map[string]any{"edition": "servico", "hours": 24 * 30, "uses": 1}, nil); s != http.StatusBadRequest {
		t.Errorf("link de 30 dias deveria ser recusado: status %d", s)
	}

	resp, err := http.Get(e.srv.URL + link.Path) // sem login
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "suporte-ti-servico.exe") ||
		!strings.HasPrefix(string(exe), "servico") {
		t.Fatalf("link: status %d, disposition %q", resp.StatusCode, resp.Header.Get("Content-Disposition"))
	}
	code := stampedCode(t, exe)
	if "/i/"+code != link.Path {
		t.Errorf("código no .exe %q não é o do link %q", code, link.Path)
	}

	if _, _, s := e.enroll(code, "PC1"); s != http.StatusCreated {
		t.Fatalf("primeiro cadastro: status %d", s)
	}
	if _, _, s := e.enroll(code, "PC2"); s != http.StatusForbidden {
		t.Errorf("link de 1 uso cadastrou duas máquinas: status %d", s)
	}
	if r, _ := http.Get(e.srv.URL + link.Path); r.StatusCode != http.StatusNotFound {
		t.Errorf("link esgotado ainda baixa: status %d", r.StatusCode)
	}

	var other struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
	}
	ana.do("POST", "/api/links", map[string]any{"edition": "portatil", "hours": 1, "uses": 5}, &other)
	if s := e.newTech("bia").do("DELETE", "/api/links/"+strconv.FormatInt(other.ID, 10), nil, nil); s != http.StatusNotFound {
		t.Errorf("outra técnica revogou o link: status %d", s)
	}
	if s := ana.do("DELETE", "/api/links/"+strconv.FormatInt(other.ID, 10), nil, nil); s != http.StatusNoContent {
		t.Fatalf("revogar: status %d", s)
	}
	if r, _ := http.Get(e.srv.URL + other.Path); r.StatusCode != http.StatusNotFound {
		t.Errorf("link revogado ainda baixa: status %d", r.StatusCode)
	}
	if r, _ := http.Get(e.srv.URL + "/i/nao-existe"); r.StatusCode != http.StatusNotFound {
		t.Errorf("link inventado: status %d", r.StatusCode)
	}

	// sem prazo e sem limite: o mesmo link serve para vários clientes
	var forever struct {
		Path      string    `json:"path"`
		MaxUses   int       `json:"maxUses"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if s := ana.do("POST", "/api/links", map[string]any{"edition": "servico", "hours": 0, "uses": 0}, &forever); s != http.StatusCreated {
		t.Fatalf("link sem prazo: status %d", s)
	}
	if forever.MaxUses < 1000 || time.Until(forever.ExpiresAt) < 50*365*24*time.Hour {
		t.Errorf("link sem prazo/limite = %+v", forever)
	}
	permanent := strings.TrimPrefix(forever.Path, "/i/")
	for i := range 3 {
		if _, _, s := e.enroll(permanent, "CLIENTE-"+strconv.Itoa(i)); s != http.StatusCreated {
			t.Fatalf("cliente %d pelo link sem limite: status %d", i, s)
		}
	}
	if n := len(ana.machines()); n != 4 {
		t.Errorf("ana deveria ter 4 máquinas (1 do primeiro link + 3), tem %d", n)
	}
}

// Senhas erradas demais bloqueiam o login por 15 minutos, inclusive a certa.
func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	c := e.anonymous()
	for i := range 10 {
		if s := c.do("POST", "/api/login", map[string]string{"username": adminUser, "password": "errada"}, nil); s != http.StatusUnauthorized {
			t.Fatalf("tentativa %d: status %d", i+1, s)
		}
	}
	if s := c.do("POST", "/api/login", map[string]string{"username": adminUser, "password": adminPass}, nil); s != http.StatusTooManyRequests {
		t.Errorf("depois de 10 erros: status %d, want 429", s)
	}
}

// Toda resposta leva as defesas do navegador.
func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"script-src 'self'", "frame-ancestors 'none'", "connect-src 'self' wss://"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP sem %q: %s", want, csp)
		}
	}
	for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
}

// Acessos abertos quando o relay parou são encerrados na partida seguinte.
func TestOrphanAccessesClosedOnStart(t *testing.T) {
	e := newEnv(t)
	execSQL(t, `INSERT INTO access_log (username, agent_id, host, remote_addr, started_at)
		VALUES ('admin', 'pc1', 'PC1', '1.2.3.4', now() - interval '1 hour')`)
	n, err := audit.New(e.db, nil).CloseOrphans(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("CloseOrphans = %d, %v; want 1", n, err)
	}
	var entries []audit.Entry
	e.login(adminUser, adminPass).do("GET", "/api/audit", nil, &entries)
	if len(entries) != 1 || entries[0].EndedAt == nil {
		t.Errorf("acesso continua aberto: %+v", entries)
	}
}
