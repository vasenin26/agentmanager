package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// SSHKeyPair represents a pair of SSH keys
type SSHKeyPair struct {
	PrivateKey string
	PublicKey  string
}

// GenerateSSHKeyPair generates a new RSA SSH key pair
// Deprecated: Use ssh.Storage instead for persistent storage
func GenerateSSHKeyPair() (*SSHKeyPair, error) {
	// Generate RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Encode private key to PEM format
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Generate public key from private key
	publicKey, err := ssh.NewPublicKey(&privateKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate public key: %w", err)
	}

	// Encode public key to OpenSSH format
	publicKeySSH := ssh.MarshalAuthorizedKey(publicKey)

	return &SSHKeyPair{
		PrivateKey: string(privateKeyPEM),
		PublicKey:  string(publicKeySSH),
	}, nil
}
