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

	// Create projects subdirectory for project keys
	projectsDir := filepath.Join(keysDir, "projects")
	if err := os.MkdirAll(projectsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create projects directory: %w", err)
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

// --- Project Key Management ---

// GenerateAndStoreProjectKeyPair generates a new SSH key pair for a project and stores it
func (s *Storage) GenerateAndStoreProjectKeyPair(projectID string) (*ProjectKeyPair, error) {
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

	keyPair := &ProjectKeyPair{
		ProjectID:  projectID,
		PrivateKey: string(privateKeyPEM),
		PublicKey:  string(publicKeySSH),
	}

	// Store keys to filesystem
	if err := s.storeProjectKeyPair(keyPair); err != nil {
		return nil, fmt.Errorf("failed to store project key pair: %w", err)
	}

	return keyPair, nil
}

// GetProjectKeyPair retrieves a key pair by project ID
func (s *Storage) GetProjectKeyPair(projectID string) (*ProjectKeyPair, error) {
	privateKeyPath := s.getProjectPrivateKeyPath(projectID)
	publicKeyPath := s.getProjectPublicKeyPath(projectID)

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

	return &ProjectKeyPair{
		ProjectID:  projectID,
		PrivateKey: string(privateKeyData),
		PublicKey:  string(publicKeyData),
	}, nil
}

// ValidateProjectPublicKey проверяет соответствие публичного ключа приватному ключу проекта
func (s *Storage) ValidateProjectPublicKey(projectID, publicKeyToValidate string) (bool, error) {
	// Получить сохраненную пару ключей
	keyPair, err := s.GetProjectKeyPair(projectID)
	if err != nil {
		return false, err
	}

	// Сравнить публичные ключи (удаляем пробелы и переносы строк для сравнения)
	storedKey := trimWhitespace(keyPair.PublicKey)
	providedKey := trimWhitespace(publicKeyToValidate)

	return storedKey == providedKey, nil
}

// ProjectKeyPairExists проверяет существование пары ключей для проекта
func (s *Storage) ProjectKeyPairExists(projectID string) bool {
	privateKeyPath := s.getProjectPrivateKeyPath(projectID)
	publicKeyPath := s.getProjectPublicKeyPath(projectID)

	_, err1 := os.Stat(privateKeyPath)
	_, err2 := os.Stat(publicKeyPath)

	return err1 == nil && err2 == nil
}

// storeProjectKeyPair saves project key pair to filesystem
func (s *Storage) storeProjectKeyPair(keyPair *ProjectKeyPair) error {
	// Store private key
	privateKeyPath := s.getProjectPrivateKeyPath(keyPair.ProjectID)
	if err := os.WriteFile(privateKeyPath, []byte(keyPair.PrivateKey), 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Store public key
	publicKeyPath := s.getProjectPublicKeyPath(keyPair.ProjectID)
	if err := os.WriteFile(publicKeyPath, []byte(keyPair.PublicKey), 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}

	return nil
}

// getProjectPrivateKeyPath returns the path for project private key file
func (s *Storage) getProjectPrivateKeyPath(projectID string) string {
	return filepath.Join(s.keysDir, "projects", fmt.Sprintf("%s_private.pem", projectID))
}

// getProjectPublicKeyPath returns the path for project public key file
func (s *Storage) getProjectPublicKeyPath(projectID string) string {
	return filepath.Join(s.keysDir, "projects", fmt.Sprintf("%s_public.pub", projectID))
}

// trimWhitespace removes all whitespace characters from a string
func trimWhitespace(s string) string {
	result := ""
	for _, c := range s {
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			result += string(c)
		}
	}
	return result
}
