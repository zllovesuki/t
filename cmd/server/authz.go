package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"strings"
)

func checkClientSNI(domain string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if !strings.HasSuffix(cs.ServerName, domain) {
			return fmt.Errorf("unauthorized domain name: %s", cs.ServerName)
		}
		return nil
	}
}

func checkPeerSAN(required string) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) != 1 {
			return errors.New("exactly one peer certificate is required")
		}
		found := false
		for _, name := range cs.PeerCertificates[0].DNSNames {
			found = found || name == required
		}
		if !found {
			return fmt.Errorf("%s must be present in SANs", required)
		}
		return nil
	}
}
