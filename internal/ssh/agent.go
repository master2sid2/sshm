package ssh

import (
	"fmt"
	"net"
	"os"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentAuthMethod connects to the ssh-agent socket and returns an auth method.
// Returns an error if SSH_AUTH_SOCK is unset or the agent is unreachable.
func agentAuthMethod() (gossh.AuthMethod, func(), error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to ssh-agent: %w", err)
	}

	ag := agent.NewClient(conn)
	method := gossh.PublicKeysCallback(ag.Signers)
	return method, func() { conn.Close() }, nil
}
