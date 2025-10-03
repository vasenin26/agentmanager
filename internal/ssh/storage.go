package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// Storage manages SSH key storage in filesystem
type Storage struct {
	keysDir string
}

// NewStorage creates a new SSH key storage
func NewStorage(keysDir string) (*Storage, error) {
	// Create keys directory if it doesn't exist
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create keys directory: %w", err)
	}
	
	return &Storage{keysDir: keysDir}, nil
}

// GenerateAndStoreKeyPair generates a new SSH key pair and stores it
func (s *Storage) GenerateAndStoreKeyPair(agentID string) (*KeyPair, error) {
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

	keyPair := &KeyPair{
		AgentID:    agentID,
		PrivateKey: string(privateKeyPEM),
		PublicKey:  string(publicKeySSH),
	}

	// Store keys to filesystem
	if err := s.storeKeyPair(keyPair); err != nil {
		return nil, fmt.Errorf("failed to store key pair: %w", err)
	}

	return keyPair, nil
}

// GetKeyPair retrieves a key pair by agent ID
func (s *Storage) GetKeyPair(agentID string) (*KeyPair, error) {
	privateKeyPath := s.getPrivateKeyPath(agentID)
	publicKeyPath := s.getPublicKeyPath(agentID)

	// Read private key
	privateKeyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	// Read public key
	publicKeyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key: %w", err)
	}

	return &KeyPair{
		AgentID:    agentID,
		PrivateKey: string(privateKeyData),
		PublicKey:  string(publicKeyData),
	}, nil
}

// DeleteKeyPair removes key pair from filesystem
func (s *Storage) DeleteKeyPair(agentID string) error {
	privateKeyPath := s.getPrivateKeyPath(agentID)
	publicKeyPath := s.getPublicKeyPath(agentID)

	// Remove private key file
	if err := os.Remove(privateKeyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove private key: %w", err)
	}

	// Remove public key file
	if err := os.Remove(publicKeyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove public key: %w", err)
	}

	return nil
}

// storeKeyPair saves key pair to filesystem
func (s *Storage) storeKeyPair(keyPair *KeyPair) error {
	// Store private key
	privateKeyPath := s.getPrivateKeyPath(keyPair.AgentID)
	if err := os.WriteFile(privateKeyPath, []byte(keyPair.PrivateKey), 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Store public key
	publicKeyPath := s.getPublicKeyPath(keyPair.AgentID)
	if err := os.WriteFile(publicKeyPath, []byte(keyPair.PublicKey), 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}

	return nil
}

// getPrivateKeyPath returns the path for private key file
func (s *Storage) getPrivateKeyPath(agentID string) string {
	return filepath.Join(s.keysDir, fmt.Sprintf("%s_private.pem", agentID))
}

// getPublicKeyPath returns the path for public key file
func (s *Storage) getPublicKeyPath(agentID string) string {
	return filepath.Join(s.keysDir, fmt.Sprintf("%s_public.pub", agentID))
}
