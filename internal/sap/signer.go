package sap

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const supportedVersion = uint32(200)

var errSignerClosed = errors.New("SAP signer is closed")

type Config struct {
	// Client is the same network client used for the Store bag and login.
	// Sharing it preserves proxy policy, transport and cookie session.
	Client         *http.Client
	SetupURL       string
	CertificateURL string
	Version        uint32
	HardwareID     []byte
}

func setupClient(config Config) *http.Client {
	if config.Client != nil {
		return config.Client
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type ActionSigner interface {
	Sign(input []byte) ([]byte, error)
	Close() error
}

func validateConfig(config Config) error {
	if config.Version != supportedVersion {
		return fmt.Errorf("unsupported SAP version %d", config.Version)
	}

	if len(config.HardwareID) == 0 || len(config.HardwareID) > 20 {
		return errors.New("SAP hardware ID must contain between 1 and 20 bytes")
	}

	if err := validateEndpoint("setup", config.SetupURL); err != nil {
		return err
	}

	if err := validateEndpoint("certificate", config.CertificateURL); err != nil {
		return err
	}

	return nil
}

func validateEndpoint(label, value string) error {
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return fmt.Errorf("SAP %s URL must be an absolute HTTPS URL", label)
	}

	return nil
}
