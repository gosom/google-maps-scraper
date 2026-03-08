package vps

import (
	"context"
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/gosom/google-maps-scraper/cryptoext"
	"github.com/gosom/google-maps-scraper/infra"
)

var _ infra.Provisioner = (*provisioner)(nil)

type provisioner struct {
	host       string
	port       string
	user       string
	privateKey []byte
}

type Option func(*provisioner)

func WithHost(host string) Option {
	return func(p *provisioner) {
		p.host = host
	}
}

func WithPort(port string) Option {
	return func(p *provisioner) {
		p.port = port
	}
}

func WithUser(user string) Option {
	return func(p *provisioner) {
		p.user = user
	}
}

func WithPrivateKey(key []byte) Option {
	return func(p *provisioner) {
		p.privateKey = key
	}
}

func New(opts ...Option) (infra.Provisioner, error) {
	ans := provisioner{
		host:       "localhost",
		port:       "22",
		user:       "root",
		privateKey: nil,
	}

	for _, opt := range opts {
		opt(&ans)
	}

	return &ans, nil
}

func (p *provisioner) CheckConnectivity(ctx context.Context) error {
	client, err := p.dial(ctx)
	if err != nil {
		return err
	}

	_ = client.Close()

	return nil
}

func (p *provisioner) ExecuteCommand(ctx context.Context, command string) (string, error) {
	client, err := p.dial(ctx)
	if err != nil {
		return "", err
	}

	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	defer func() { _ = session.Close() }()

	output, err := session.CombinedOutput(command)
	if err != nil {
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	return string(output), nil
}

func (p *provisioner) Deploy(ctx context.Context, cfg *infra.DeployConfig) error {
	script := GenerateDeployScript(DeployScriptConfig{
		RegistryURL:   cfg.Registry.URL,
		RegistryUser:  cfg.Registry.Username,
		RegistryToken: cfg.Registry.Token,
		ImageName:     cfg.Registry.Image,
		DatabaseURL:   cfg.DatabaseURL,
		EncryptionKey: cfg.EncryptionKey,
		HashSalt:      cfg.HashSalt,
	})

	output, err := p.ExecuteCommand(ctx, WrapScript(script))
	if err != nil {
		return fmt.Errorf("deployment failed: %s\n%w", output, err)
	}

	return nil
}

func (p *provisioner) CreateDatabase(ctx context.Context) (*infra.DatabaseInfo, error) {
	password, err := cryptoext.GenerateRandomHexString(16)
	if err != nil {
		return nil, fmt.Errorf("failed to generate password: %w", err)
	}

	script := GenerateDBScript(password)

	output, err := p.ExecuteCommand(ctx, WrapScript(script))
	if err != nil {
		return nil, fmt.Errorf("database setup failed: %s\n%w", output, err)
	}

	connURL := fmt.Sprintf("postgres://gms:%s@%s:5432/gms?sslmode=require", password, p.host)

	return &infra.DatabaseInfo{
		ConnectionURL: connURL,
	}, nil
}

func (p *provisioner) dial(ctx context.Context) (*ssh.Client, error) {
	if len(p.privateKey) == 0 {
		return nil, fmt.Errorf("%w: private key not set", infra.ErrConnectionFailed)
	}

	signer, err := ssh.ParsePrivateKey(p.privateKey)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse private key: %v", infra.ErrConnectionFailed, err)
	}

	addr := net.JoinHostPort(p.host, p.port)

	hostKeyCallback, err := infra.NewTOFUHostKeyCallback(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to initialize host key verification: %v", infra.ErrConnectionFailed, err)
	}

	config := &ssh.ClientConfig{
		User: p.user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         30 * time.Second,
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", infra.ErrConnectionFailed, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: SSH handshake failed: %v", infra.ErrConnectionFailed, err)
	}

	return ssh.NewClient(sshConn, chans, reqs), nil
}
