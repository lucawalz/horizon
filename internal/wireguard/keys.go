package wireguard

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

type Keypair struct {
	PrivateKey string
	PublicKey  string
}

func GenerateKeypair() (Keypair, error) {
	priv := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(priv); err != nil {
		return Keypair{}, fmt.Errorf("wireguard: read random: %w", err)
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return Keypair{}, fmt.Errorf("wireguard: derive public key: %w", err)
	}
	return Keypair{
		PrivateKey: base64.StdEncoding.EncodeToString(priv),
		PublicKey:  base64.StdEncoding.EncodeToString(pub),
	}, nil
}

func PublicFromPrivate(privB64 string) (string, error) {
	priv, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return "", fmt.Errorf("wireguard: decode private key: %w", err)
	}
	if len(priv) != curve25519.ScalarSize {
		return "", fmt.Errorf("wireguard: private key must be %d bytes, got %d", curve25519.ScalarSize, len(priv))
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("wireguard: derive public key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(pub), nil
}
