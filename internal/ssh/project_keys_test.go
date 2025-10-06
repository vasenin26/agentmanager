package ssh_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vasenin26/agentmanager/internal/ssh"
)

func TestProjectKeyManager_GenerateKeyPair(t *testing.T) {
	// Создать временную директорию
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Создать менеджер
	manager, err := ssh.NewProjectKeyManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create ProjectKeyManager: %v", err)
	}

	// Генерировать ключи
	projectID := "project-123"
	privateKey, publicKey, err := manager.GenerateKeyPair(projectID)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Проверить что ключи не пустые
	if privateKey == "" {
		t.Error("Private key is empty")
	}
	if publicKey == "" {
		t.Error("Public key is empty")
	}

	// Проверить формат приватного ключа
	if !strings.Contains(privateKey, "BEGIN RSA PRIVATE KEY") {
		t.Error("Private key doesn't have expected format")
	}

	// Проверить формат публичного ключа (OpenSSH)
	if !strings.HasPrefix(publicKey, "ssh-rsa ") {
		t.Error("Public key doesn't have expected format (ssh-rsa)")
	}

	// Проверить существование файлов
	privateKeyPath := filepath.Join(tmpDir, "projects", "project-123_private.pem")
	publicKeyPath := filepath.Join(tmpDir, "projects", "project-123_public.pub")

	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Error("Private key file not created")
	}
	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Error("Public key file not created")
	}

	// Проверить права доступа к приватному ключу
	info, err := os.Stat(privateKeyPath)
	if err != nil {
		t.Fatalf("Failed to stat private key: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("Private key permissions are %o, expected 0600", info.Mode().Perm())
	}

	// Проверить права доступа к публичному ключу
	info, err = os.Stat(publicKeyPath)
	if err != nil {
		t.Fatalf("Failed to stat public key: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("Public key permissions are %o, expected 0644", info.Mode().Perm())
	}
}

func TestProjectKeyManager_GetKeyPair(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager, err := ssh.NewProjectKeyManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create ProjectKeyManager: %v", err)
	}

	projectID := "project-456"

	// Генерировать ключи
	originalPrivate, originalPublic, err := manager.GenerateKeyPair(projectID)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Получить ключи
	retrievedPrivate, retrievedPublic, err := manager.GetKeyPair(projectID)
	if err != nil {
		t.Fatalf("Failed to get key pair: %v", err)
	}

	// Проверить соответствие
	if originalPrivate != retrievedPrivate {
		t.Error("Retrieved private key doesn't match original")
	}
	if originalPublic != retrievedPublic {
		t.Error("Retrieved public key doesn't match original")
	}
}

func TestProjectKeyManager_GetKeyPair_NotFound(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager, err := ssh.NewProjectKeyManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create ProjectKeyManager: %v", err)
	}

	// Попытаться получить несуществующие ключи
	_, _, err = manager.GetKeyPair("non-existent-project")
	if err == nil {
		t.Error("Expected error for non-existent project, got nil")
	}
}

func TestProjectKeyManager_KeyPairExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager, err := ssh.NewProjectKeyManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create ProjectKeyManager: %v", err)
	}

	projectID := "project-789"

	// Проверить что ключей еще нет
	if manager.KeyPairExists(projectID) {
		t.Error("KeyPairExists returned true for non-existent project")
	}

	// Генерировать ключи
	_, _, err = manager.GenerateKeyPair(projectID)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Проверить что ключи теперь существуют
	if !manager.KeyPairExists(projectID) {
		t.Error("KeyPairExists returned false for existing project")
	}
}

func TestProjectKeyManager_ValidatePublicKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager, err := ssh.NewProjectKeyManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create ProjectKeyManager: %v", err)
	}

	projectID := "project-validation"
	_, publicKey, err := manager.GenerateKeyPair(projectID)
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Валидный ключ
	isValid, err := manager.ValidatePublicKey(projectID, publicKey)
	if err != nil {
		t.Fatalf("Failed to validate public key: %v", err)
	}
	if !isValid {
		t.Error("ValidatePublicKey returned false for matching key")
	}

	// Валидный ключ с пробелами (должен нормализоваться)
	isValid, err = manager.ValidatePublicKey(projectID, "  "+publicKey+"  \n")
	if err != nil {
		t.Fatalf("Failed to validate public key with whitespace: %v", err)
	}
	if !isValid {
		t.Error("ValidatePublicKey returned false for matching key with whitespace")
	}

	// Невалидный ключ
	isValid, err = manager.ValidatePublicKey(projectID, "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCtest")
	if err != nil {
		t.Fatalf("Failed to validate invalid public key: %v", err)
	}
	if isValid {
		t.Error("ValidatePublicKey returned true for non-matching key")
	}
}

func TestProjectKeyManager_MultipleProjects(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ssh-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	manager, err := ssh.NewProjectKeyManager(tmpDir)
	if err != nil {
		t.Fatalf("Failed to create ProjectKeyManager: %v", err)
	}

	// Генерировать ключи для нескольких проектов
	projects := []string{"project-1", "project-2", "project-3"}
	keys := make(map[string]struct{ private, public string })

	for _, projectID := range projects {
		private, public, err := manager.GenerateKeyPair(projectID)
		if err != nil {
			t.Fatalf("Failed to generate key pair for %s: %v", projectID, err)
		}
		keys[projectID] = struct{ private, public string }{private, public}
	}

	// Проверить что все проекты существуют
	for _, projectID := range projects {
		if !manager.KeyPairExists(projectID) {
			t.Errorf("Project %s doesn't exist", projectID)
		}
	}

	// Проверить что ключи разные для разных проектов
	if keys["project-1"].private == keys["project-2"].private {
		t.Error("Different projects have the same private key")
	}
	if keys["project-1"].public == keys["project-2"].public {
		t.Error("Different projects have the same public key")
	}

	// Проверить что можем получить ключи каждого проекта
	for _, projectID := range projects {
		private, public, err := manager.GetKeyPair(projectID)
		if err != nil {
			t.Fatalf("Failed to get key pair for %s: %v", projectID, err)
		}
		if private != keys[projectID].private {
			t.Errorf("Retrieved private key for %s doesn't match", projectID)
		}
		if public != keys[projectID].public {
			t.Errorf("Retrieved public key for %s doesn't match", projectID)
		}
	}
}
