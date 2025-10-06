package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// ProjectKeyManager управляет SSH ключами на уровне проектов
// Ключи генерируются ОДИН РАЗ для проекта и используются ВСЕМИ агентами этого проекта
type ProjectKeyManager struct {
	keysDir string // Директория для хранения ключей проектов (keys/projects/)
}

// NewProjectKeyManager создает новый менеджер ключей проектов
func NewProjectKeyManager(keysDir string) (*ProjectKeyManager, error) {
	// Создать директорию для ключей проектов
	projectKeysDir := filepath.Join(keysDir, "projects")
	if err := os.MkdirAll(projectKeysDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create projects keys directory: %w", err)
	}

	return &ProjectKeyManager{
		keysDir: projectKeysDir,
	}, nil
}

// GenerateKeyPair генерирует пару SSH ключей для проекта
// ВАЖНО: Вызывается ОДИН РАЗ для проекта оркестратором
func (m *ProjectKeyManager) GenerateKeyPair(projectID string) (privateKey, publicKey string, err error) {
	// Генерировать RSA ключ
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate RSA key: %w", err)
	}

	// Приватный ключ в PEM формате
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	// Публичный ключ в OpenSSH формате
	publicSSHKey, err := ssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate public key: %w", err)
	}
	publicKeySSH := ssh.MarshalAuthorizedKey(publicSSHKey)

	// Сохранить ключи в файловую систему
	if err := m.saveKeys(projectID, privateKeyPEM, publicKeySSH); err != nil {
		return "", "", err
	}

	return string(privateKeyPEM), strings.TrimSpace(string(publicKeySSH)), nil
}

// GetKeyPair получает существующую пару ключей проекта
func (m *ProjectKeyManager) GetKeyPair(projectID string) (privateKey, publicKey string, err error) {
	privateKeyPath := m.getPrivateKeyPath(projectID)
	publicKeyPath := m.getPublicKeyPath(projectID)

	// Проверить существование ключей
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		return "", "", fmt.Errorf("project keys not found for project %s", projectID)
	}

	// Читать приватный ключ
	privateKeyBytes, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read private key: %w", err)
	}

	// Читать публичный ключ
	publicKeyBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read public key: %w", err)
	}

	return string(privateKeyBytes), strings.TrimSpace(string(publicKeyBytes)), nil
}

// KeyPairExists проверяет существование ключей проекта
func (m *ProjectKeyManager) KeyPairExists(projectID string) bool {
	privateKeyPath := m.getPrivateKeyPath(projectID)
	_, err := os.Stat(privateKeyPath)
	return err == nil
}

// ValidatePublicKey проверяет соответствие публичного ключа
func (m *ProjectKeyManager) ValidatePublicKey(projectID, publicKeyToValidate string) (bool, error) {
	_, existingPublicKey, err := m.GetKeyPair(projectID)
	if err != nil {
		return false, err
	}

	// Нормализовать ключи (убрать пробелы и переносы строк)
	existingNormalized := strings.TrimSpace(existingPublicKey)
	validateNormalized := strings.TrimSpace(publicKeyToValidate)

	return existingNormalized == validateNormalized, nil
}

// saveKeys сохраняет ключи в файловую систему
func (m *ProjectKeyManager) saveKeys(projectID string, privateKey, publicKey []byte) error {
	privateKeyPath := m.getPrivateKeyPath(projectID)
	publicKeyPath := m.getPublicKeyPath(projectID)

	// Сохранить приватный ключ (права 600 - только владелец, чтение/запись)
	if err := os.WriteFile(privateKeyPath, privateKey, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Сохранить публичный ключ (права 644 - владелец: чтение/запись, остальные: чтение)
	if err := os.WriteFile(publicKeyPath, publicKey, 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}

	return nil
}

// getPrivateKeyPath возвращает путь к приватному ключу проекта
func (m *ProjectKeyManager) getPrivateKeyPath(projectID string) string {
	return filepath.Join(m.keysDir, fmt.Sprintf("%s_private.pem", projectID))
}

// getPublicKeyPath возвращает путь к публичному ключу проекта
func (m *ProjectKeyManager) getPublicKeyPath(projectID string) string {
	return filepath.Join(m.keysDir, fmt.Sprintf("%s_public.pub", projectID))
}
