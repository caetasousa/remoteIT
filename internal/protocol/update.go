package protocol

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
)

// Atualização automática do agente (docs/DECISOES.md, ADR 0011).
//
// O build.sh publica, junto dos .exe, um manifesto (update.json) com a versão
// e, para cada edição, o SHA-256 do .exe e uma assinatura Ed25519 de
// [UpdateMessage] feita com a chave do projeto. A chave privada fica só na
// máquina de build; o agente traz a chave pública embutida. Assim um relay
// invadido não consegue empurrar um .exe próprio para as máquinas.

// UpdateFile é uma edição no manifesto.
type UpdateFile struct {
	SHA256    string `json:"sha256"`    // hex do .exe sem código gravado
	Signature string `json:"signature"` // base64 da assinatura de UpdateMessage
	Size      int64  `json:"size"`
}

// UpdateManifest é o update.json publicado pelo build.sh.
type UpdateManifest struct {
	Version string                `json:"version"`
	Files   map[string]UpdateFile `json:"files"` // por edição: "portatil", "servico"
}

// UpdateOffer é a resposta do relay a um agente: a versão disponível da edição dele.
type UpdateOffer struct {
	Version string `json:"version"`
	UpdateFile
}

// UpdateMessage é o texto assinado: amarra edição, versão e conteúdo. Mudar
// qualquer um dos três invalida a assinatura.
func UpdateMessage(edition, version, sha256hex string) []byte {
	return []byte("remoteit-update-v1\n" + edition + "\n" + version + "\n" + sha256hex)
}

// ErrBadSignature significa que a oferta não foi assinada pela chave do projeto.
var ErrBadSignature = errors.New("protocol: assinatura da atualização inválida")

// VerifyUpdate confere a assinatura de uma oferta com a chave pública pub.
func VerifyUpdate(pub ed25519.PublicKey, edition string, o UpdateOffer) error {
	sig, err := base64.StdEncoding.DecodeString(o.Signature)
	if err != nil || len(pub) != ed25519.PublicKeySize ||
		!ed25519.Verify(pub, UpdateMessage(edition, o.Version, o.SHA256), sig) {
		return ErrBadSignature
	}
	return nil
}
