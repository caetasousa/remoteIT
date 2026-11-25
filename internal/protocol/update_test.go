package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func TestVerifyUpdate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	offer := UpdateOffer{Version: "2026.11.25.0100", UpdateFile: UpdateFile{SHA256: "abc"}}
	offer.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, UpdateMessage("servico", offer.Version, offer.SHA256)))
	if err := VerifyUpdate(pub, "servico", offer); err != nil {
		t.Fatalf("oferta legítima recusada: %v", err)
	}

	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	tampered := offer
	tampered.SHA256 = "outro-exe"
	older := offer
	older.Version = "2020.01.01.0000"
	cases := map[string]error{
		"outra chave":          VerifyUpdate(otherPub, "servico", offer),
		"outro .exe":           VerifyUpdate(pub, "servico", tampered),
		"outra versão":         VerifyUpdate(pub, "servico", older),
		"assinada p/ portátil": VerifyUpdate(pub, "portatil", offer),
		"sem chave no build":   VerifyUpdate(nil, "servico", offer),
	}
	for name, err := range cases {
		if !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: err = %v, want ErrBadSignature", name, err)
		}
	}
}
