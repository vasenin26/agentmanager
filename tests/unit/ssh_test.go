package unit

import (
	"strings"
	"testing"

	"github.com/vasenin26/agentmanager/internal/crypto"
)

func TestGenerateSSHKeyPair(t *testing.T) {
	keyPair, err := crypto.GenerateSSHKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate SSH key pair: %v", err)
	}

	// Check that private key is not empty
	if keyPair.PrivateKey == "" {
		t.Error("Private key should not be empty")
	}

	// Check that public key is not empty
	if keyPair.PublicKey == "" {
		t.Error("Public key should not be empty")
	}

	// Check that private key contains RSA PRIVATE KEY header
	if !strings.Contains(keyPair.PrivateKey, "-----BEGIN RSA PRIVATE KEY-----") {
		t.Error("Private key should contain RSA PRIVATE KEY header")
	}

	// Check that public key contains ssh-rsa
	if !strings.Contains(keyPair.PublicKey, "ssh-rsa") {
		t.Error("Public key should contain ssh-rsa")
	}

	// Check that keys are different
	if keyPair.PrivateKey == keyPair.PublicKey {
		t.Error("Private and public keys should be different")
	}
}

func TestGenerateSSHKeyPairUniqueness(t *testing.T) {
	keyPair1, err1 := crypto.GenerateSSHKeyPair()
	if err1 != nil {
		t.Fatalf("Failed to generate first SSH key pair: %v", err1)
	}

	keyPair2, err2 := crypto.GenerateSSHKeyPair()
	if err2 != nil {
		t.Fatalf("Failed to generate second SSH key pair: %v", err2)
	}

	// Check that different key pairs are generated
	if keyPair1.PrivateKey == keyPair2.PrivateKey {
		t.Error("Different key pairs should have different private keys")
	}

	if keyPair1.PublicKey == keyPair2.PublicKey {
		t.Error("Different key pairs should have different public keys")
	}
}
