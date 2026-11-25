// Package machine dá ao agente uma identidade própria perante o relay.
//
// Na primeira vez que roda, o agente troca o código de cadastro (gravado no
// .exe pelo relay no download, veja internal/relay/agentbin) por uma
// credencial só desta máquina, e a guarda protegida pelo Windows (DPAPI). A
// partir daí conecta com ela. O código diz de qual técnico a máquina é; a
// credencial prova que esta máquina é ela mesma.
//
// Sem código nem credencial, o agente pode cair para a chave compartilhada
// dos agentes legados (só administradores veem essas máquinas).
package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"remoteit/internal/agent/relayclient"
	"remoteit/internal/protocol"
)

// enrollSlot é o espaço que o relay preenche com o código no download. É
// uma constante única no binário: o relay a acha pelo marcador. Nunca use
// protocol.EnrollMarker sozinho neste pacote — o marcador apareceria duas
// vezes no .exe e o relay recusaria gravar o código.
var enrollSlot = protocol.EnrollMarker + protocol.EnrollEmpty

// EmbeddedCode devolve o código de cadastro gravado no .exe, ou "" num
// build que não passou pelo download do relay.
func EmbeddedCode() string {
	return strings.TrimRight(enrollSlot[len(protocol.EnrollMarker):], string(protocol.EnrollPad))
}

// Credential é a identidade desta máquina no relay.
type Credential struct {
	MachineID string `json:"id"`
	Secret    string `json:"secret"`
	// CodeHash é o SHA-256 do código usado no cadastro. Um .exe com outro
	// código (de outro técnico) cadastra a máquina de novo, no nome dele.
	CodeHash string `json:"codeHash"`
}

func codeHash(code string) string {
	h := sha256.Sum256([]byte(code))
	return hex.EncodeToString(h[:])
}

// Store guarda a credencial entre execuções.
type Store interface {
	Load() (Credential, bool)
	Save(Credential) error
	Clear() error
}

// Keeper fornece ao relayclient as credenciais da conexão, cadastrando a
// máquina quando preciso. Implementa [relayclient.Auth].
type Keeper struct {
	RelayURL string // wss://dominio
	Code     string // código de cadastro ("" sem código)
	Name     string // nome do computador
	Store    Store
	Enroll   func(ctx context.Context, relayURL, code, name string) (Credential, error)
	// LegacyID e LegacyKey: modo antigo, usado só sem código nem credencial.
	LegacyID, LegacyKey string
	Logger              *slog.Logger

	mu       sync.Mutex
	codeDead bool // o relay recusou o código: não insistir a cada reconexão
}

var _ relayclient.Auth = (*Keeper)(nil)

// ErrNoIdentity significa que o agente não tem como se identificar: sem
// código válido, sem credencial e sem a chave legada.
var ErrNoIdentity = errors.New("machine: sem código de cadastro, credencial ou chave")

// Credentials devolve como esta conexão deve se identificar.
func (k *Keeper) Credentials(ctx context.Context) (relayclient.Credentials, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	cred, ok := k.Store.Load()
	if ok && (k.Code == "" || cred.CodeHash == codeHash(k.Code)) {
		return relayclient.Credentials{MachineID: cred.MachineID, Secret: cred.Secret}, nil
	}
	if k.Code != "" && !k.codeDead {
		fresh, err := k.Enroll(ctx, k.RelayURL, k.Code, k.Name)
		switch {
		case err == nil:
			fresh.CodeHash = codeHash(k.Code)
			if err := k.Store.Save(fresh); err != nil {
				k.Logger.Error("não foi possível guardar a credencial da máquina", "err", err)
			}
			k.Logger.Info("máquina cadastrada", "id", fresh.MachineID)
			return relayclient.Credentials{MachineID: fresh.MachineID, Secret: fresh.Secret}, nil
		case errors.Is(err, ErrCodeRejected):
			k.codeDead = true
			k.Logger.Info("código de cadastro recusado (vencido, esgotado ou revogado)")
		default:
			return relayclient.Credentials{}, err // rede: tenta de novo no backoff
		}
	}
	if ok { // código recusado, mas a máquina já tinha credencial
		return relayclient.Credentials{MachineID: cred.MachineID, Secret: cred.Secret}, nil
	}
	if k.LegacyKey != "" {
		return relayclient.Credentials{LegacyID: k.LegacyID, LegacyKey: k.LegacyKey}, nil
	}
	return relayclient.Credentials{}, ErrNoIdentity
}

// Rejected é chamado quando o relay recusa a conexão. 401: a credencial não
// vale (banco recriado, por exemplo); ela é apagada e, com código, a máquina
// se cadastra de novo. 403: a máquina foi removida; nada muda e o agente
// segue tentando no intervalo máximo, sem voltar sozinho.
func (k *Keeper) Rejected(status int) {
	if status != 401 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.Store.Load(); ok {
		k.Logger.Info("credencial da máquina recusada; será cadastrada de novo")
		k.Store.Clear()
		k.codeDead = false
	}
}
