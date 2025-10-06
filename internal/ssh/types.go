package ssh

// KeyPair represents a pair of SSH keys with agent ID
type KeyPair struct {
	AgentID    string
	PrivateKey string
	PublicKey  string
}

// ProjectKeyPair represents a pair of SSH keys for a project
type ProjectKeyPair struct {
	ProjectID  string
	PrivateKey string
	PublicKey  string
}
