package installercontract

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type ed25519Vector struct {
	Algorithm          string `json:"algorithm"`
	PublicKeyHex       string `json:"publicKeyHex"`
	PayloadHex         string `json:"payloadHex"`
	PayloadBase64URL   string `json:"payloadBase64url"`
	SignatureHex       string `json:"signatureHex"`
	SignatureBase64URL string `json:"signatureBase64url"`
}

func TestEd25519EnvelopeVectorMatchesGoRuntime(t *testing.T) {
	raw, err := os.ReadFile("desktop/tests/fixtures/ed25519-rfc8032-test-2.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector ed25519Vector
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	publicKey, err := hex.DecodeString(vector.PublicKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := hex.DecodeString(vector.PayloadHex)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := hex.DecodeString(vector.SignatureHex)
	if err != nil {
		t.Fatal(err)
	}
	if vector.Algorithm != "Ed25519" || base64.RawURLEncoding.EncodeToString(payload) != vector.PayloadBase64URL || base64.RawURLEncoding.EncodeToString(signature) != vector.SignatureBase64URL {
		t.Fatal("vector encoding mismatch")
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		t.Fatal("RFC 8032 vector signature rejected")
	}
	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 1
	if ed25519.Verify(ed25519.PublicKey(publicKey), tampered, signature) {
		t.Fatal("tampered vector accepted")
	}
}
