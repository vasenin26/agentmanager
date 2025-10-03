package unit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vasenin26/agentmanager/internal/ssh"
)

func TestSSHStorage(t *testing.T) {
	// Create temporary directory for testing
	tempDir := t.TempDir()
	
	// Create SSH storage
	storage, err := ssh.NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SSH storage: %v", err)
	}

	agentID := "test-agent-123"

	// Test generating and storing key pair
	keyPair, err := storage.GenerateAndStoreKeyPair(agentID)
	if err != nil {
		t.Fatalf("Failed to generate and store key pair: %v", err)
	}

	// Check that key pair has correct agent ID
	if keyPair.AgentID != agentID {
		t.Errorf("Expected agent ID %s, got %s", agentID, keyPair.AgentID)
	}

	// Check that keys are not empty
	if keyPair.PrivateKey == "" {
		t.Error("Private key should not be empty")
	}

	if keyPair.PublicKey == "" {
		t.Error("Public key should not be empty")
	}

	// Test retrieving key pair
	retrievedKeyPair, err := storage.GetKeyPair(agentID)
	if err != nil {
		t.Fatalf("Failed to retrieve key pair: %v", err)
	}

	// Check that retrieved keys match original
	if retrievedKeyPair.AgentID != keyPair.AgentID {
		t.Errorf("Expected agent ID %s, got %s", keyPair.AgentID, retrievedKeyPair.AgentID)
	}

	if retrievedKeyPair.PrivateKey != keyPair.PrivateKey {
		t.Error("Retrieved private key does not match original")
	}

	if retrievedKeyPair.PublicKey != keyPair.PublicKey {
		t.Error("Retrieved public key does not match original")
	}

	// Test file permissions
	privateKeyPath := filepath.Join(tempDir, agentID+"_private.pem")
	publicKeyPath := filepath.Join(tempDir, agentID+"_public.pub")

	// Check that files exist
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Error("Private key file should exist")
	}

	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Error("Public key file should exist")
	}

	// Test that keys persist (they should not be deleted automatically)
	// In the new implementation, keys are preserved for agent reuse
}

func TestSSHStorageMultipleAgents(t *testing.T) {
	// Create temporary directory for testing
	tempDir := t.TempDir()
	
	// Create SSH storage
	storage, err := ssh.NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SSH storage: %v", err)
	}

	agentIDs := []string{"agent-1", "agent-2", "agent-3"}
	keyPairs := make(map[string]*ssh.KeyPair)

	// Generate keys for multiple agents
	for _, agentID := range agentIDs {
		keyPair, err := storage.GenerateAndStoreKeyPair(agentID)
		if err != nil {
			t.Fatalf("Failed to generate key pair for agent %s: %v", agentID, err)
		}
		keyPairs[agentID] = keyPair
	}

	// Verify all keys are different
	for i, agentID1 := range agentIDs {
		for j, agentID2 := range agentIDs {
			if i != j {
				keyPair1 := keyPairs[agentID1]
				keyPair2 := keyPairs[agentID2]
				
				if keyPair1.PrivateKey == keyPair2.PrivateKey {
					t.Errorf("Private keys for agents %s and %s should be different", agentID1, agentID2)
				}
				
				if keyPair1.PublicKey == keyPair2.PublicKey {
					t.Errorf("Public keys for agents %s and %s should be different", agentID1, agentID2)
				}
			}
		}
	}

	// Test retrieving all keys
	for _, agentID := range agentIDs {
		retrievedKeyPair, err := storage.GetKeyPair(agentID)
		if err != nil {
			t.Fatalf("Failed to retrieve key pair for agent %s: %v", agentID, err)
		}
		
		originalKeyPair := keyPairs[agentID]
		if retrievedKeyPair.PrivateKey != originalKeyPair.PrivateKey {
			t.Errorf("Retrieved private key for agent %s does not match original", agentID)
		}
		
		if retrievedKeyPair.PublicKey != originalKeyPair.PublicKey {
			t.Errorf("Retrieved public key for agent %s does not match original", agentID)
		}
	}
}

func TestSSHStorageKeyReuse(t *testing.T) {
	// Create temporary directory for testing
	tempDir := t.TempDir()
	
	// Create SSH storage
	storage, err := ssh.NewStorage(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SSH storage: %v", err)
	}

	agentID := "test-agent-reuse"

	// Generate key pair for the first time
	keyPair1, err := storage.GenerateAndStoreKeyPair(agentID)
	if err != nil {
		t.Fatalf("Failed to generate first key pair: %v", err)
	}

	// Try to get the same key pair (should reuse existing)
	keyPair2, err := storage.GetKeyPair(agentID)
	if err != nil {
		t.Fatalf("Failed to retrieve existing key pair: %v", err)
	}

	// Keys should be identical
	if keyPair1.PrivateKey != keyPair2.PrivateKey {
		t.Error("Private keys should be identical when reusing")
	}

	if keyPair1.PublicKey != keyPair2.PublicKey {
		t.Error("Public keys should be identical when reusing")
	}

	if keyPair1.AgentID != keyPair2.AgentID {
		t.Error("Agent IDs should be identical")
	}
}
