package acme

import "fmt"

var (
	ErrNoAccount     = fmt.Errorf("no account found associated with CertManager")
	ErrAccountExists = fmt.Errorf("already have account associated with CertManager")
	ErrNoPKey        = fmt.Errorf("no private key associated with CertManager")
	ErrPKeyExists    = fmt.Errorf("already have private key associated with CertManager")
	ErrNoCert        = fmt.Errorf("no cert was generated")
)
