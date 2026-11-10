package twofactor

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig is the minimal transport configuration for the code mailer.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	Timeout  time.Duration
}

// SMTPMailer sends code emails through a configured relay. STARTTLS is
// attempted when advertised; localhost development may disable it.
type SMTPMailer struct {
	config SMTPConfig
	dialer func(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error)
}

func NewSMTPMailer(config SMTPConfig) *SMTPMailer {
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	return &SMTPMailer{
		config: config,
		dialer: func(ctx context.Context, network, address string, timeout time.Duration) (net.Conn, error) {
			dialer := &net.Dialer{Timeout: timeout}
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func (m *SMTPMailer) Send(ctx context.Context, to, subject, body string) error {
	if strings.TrimSpace(m.config.Host) == "" || m.config.Port == 0 || strings.TrimSpace(to) == "" {
		return fmt.Errorf("smtp mailer is not fully configured")
	}
	address := net.JoinHostPort(m.config.Host, strconv.Itoa(m.config.Port))
	connection, err := m.dialer(ctx, "tcp", address, m.config.Timeout)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer func() { _ = connection.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(connection, m.config.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() { _ = client.Quit() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: m.config.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if m.config.Username != "" {
		auth := smtp.PlainAuth("", m.config.Username, m.config.Password, m.config.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(m.config.From); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n",
		m.config.From, to, subject, body)
	if _, err := writer.Write([]byte(message)); err != nil {
		return fmt.Errorf("write email: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}
