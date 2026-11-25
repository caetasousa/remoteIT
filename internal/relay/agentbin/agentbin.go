// Package agentbin grava o código de cadastro do técnico no .exe do agente,
// no momento do download, sem recompilar.
//
// O agente reserva um espaço fixo no binário (veja
// [protocol.EnrollMarker]); [Stamp] acha esse espaço e escreve o código nele.
// O resultado é um .exe que, ao rodar pela primeira vez, cadastra a máquina
// no nome de quem baixou.
package agentbin

import (
	"bytes"
	"errors"
	"strings"

	"remoteit/internal/protocol"
)

// ErrNoSlot significa que o binário não tem exatamente um espaço reservado:
// é um build antigo, outro arquivo ou um build quebrado.
var ErrNoSlot = errors.New("agentbin: o .exe não tem o espaço do código de cadastro")

// ErrBadCode significa um código vazio, longo demais ou com caracteres que
// o agente não lê de volta.
var ErrBadCode = errors.New("agentbin: código de cadastro inválido")

// Stamp devolve uma cópia de exe com code gravado no espaço reservado. exe
// não é alterado.
func Stamp(exe []byte, code string) ([]byte, error) {
	if code == "" || len(code) > protocol.EnrollCodeLen || strings.ContainsRune(code, protocol.EnrollPad) {
		return nil, ErrBadCode
	}
	marker := []byte(protocol.EnrollMarker)
	i := bytes.Index(exe, marker)
	if i < 0 || bytes.Count(exe, marker) != 1 || i+len(marker)+protocol.EnrollCodeLen > len(exe) {
		return nil, ErrNoSlot
	}
	out := bytes.Clone(exe)
	slot := out[i+len(marker) : i+len(marker)+protocol.EnrollCodeLen]
	n := copy(slot, code)
	for j := n; j < len(slot); j++ {
		slot[j] = protocol.EnrollPad
	}
	return out, nil
}
