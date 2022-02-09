package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/eggsampler/acme/v3"
)

type CertManager struct {
	client acme.Client
	config Config

	accMu   sync.RWMutex
	accKey  *ecdsa.PrivateKey
	account acme.Account
	hasAcc  bool

	certPKeyMu  sync.RWMutex
	certPKey    *ecdsa.PrivateKey
	hasCertPKey bool

	certMu     sync.RWMutex
	hasCert    bool
	storedCert tls.Certificate
	storedPem  Bundle
}

type AccountFile struct {
	PrivateKey string
	URL        string
}

type Config struct {
	Directory   string
	DNSProvider Provider
	Contact     string
	DataDir     string
	RootZone    string
	Domain      string
}

type Bundle struct {
	PrivateKey string
	Chain      []string
}

func New(conf Config) (*CertManager, error) {
	if !strings.HasSuffix(conf.Domain, conf.RootZone) {
		return nil, errors.New("domain must be under root zone")
	}
	client, err := acme.NewClient(conf.Directory, acme.WithHTTPTimeout(time.Second*10))
	if err != nil {
		return nil, fmt.Errorf("initializing acme client: %w", err)
	}
	c := &CertManager{
		client: client,
		config: conf,
	}
	return c, nil
}

func (c *CertManager) CreateAccount() error {
	c.accMu.Lock()
	defer c.accMu.Unlock()

	if c.hasAcc {
		return ErrAccountExists
	}

	var err error
	c.accKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating new account private key: %w", err)
	}
	c.account, err = c.client.NewAccountOptions(c.accKey, acme.NewAcctOptAgreeTOS(), acme.NewAcctOptWithContacts(c.config.Contact))
	if err != nil {
		return fmt.Errorf("creating account with CA: %w", err)
	}
	c.hasAcc = true

	pem, err := keyToPEM(c.accKey)
	if err != nil {
		return fmt.Errorf("converting pkey to pem: %w", err)
	}
	af := AccountFile{
		PrivateKey: string(pem),
		URL:        c.account.URL,
	}
	return c.persistAccount(af)
}

func (c *CertManager) persistAccount(af AccountFile) error {
	w, err := os.Create(path.Join(c.config.DataDir, "accounts.json"))
	if err != nil {
		return fmt.Errorf("opening accounts.json for writing: %w", err)
	}
	defer w.Close()
	err = json.NewEncoder(w).Encode(&af)
	if err != nil {
		return fmt.Errorf("writing to accounts.json: %w", err)
	}
	return nil
}

func (c *CertManager) persisCerts(bundle Bundle) error {
	w, err := os.Create(path.Join(c.config.DataDir, "bundle.json"))
	if err != nil {
		return fmt.Errorf("opening bundle.json for writing: %w", err)
	}
	defer w.Close()
	err = json.NewEncoder(w).Encode(&bundle)
	if err != nil {
		return fmt.Errorf("writing to bundle.json: %w", err)
	}
	return nil
}

func (c *CertManager) ExportAccount() (*AccountFile, error) {
	c.accMu.RLock()
	defer c.accMu.RUnlock()

	if !c.hasAcc {
		return nil, ErrNoAccount
	}
	pem, err := keyToPEM(c.accKey)
	if err != nil {
		return nil, fmt.Errorf("converting pkey to pem: %w", err)
	}
	af := AccountFile{
		PrivateKey: string(pem),
		URL:        c.account.URL,
	}
	return &af, nil
}

func (c *CertManager) LoadAccountFromFile() error {
	f, err := os.Open(path.Join(c.config.DataDir, "accounts.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoAccount
		}
		return fmt.Errorf("loading accounts.json: %w", err)
	}
	defer f.Close()

	var af AccountFile
	err = json.NewDecoder(f).Decode(&af)
	if err != nil {
		return fmt.Errorf("decoding accounts.json: %w", err)
	}

	return c.ImportAccount(af, false)
}

func (c *CertManager) LoadBundleFromFile() error {
	f, err := os.Open(path.Join(c.config.DataDir, "bundle.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNoCert
		}
		return fmt.Errorf("loading bundle.json: %w", err)
	}
	defer f.Close()

	var bundle Bundle
	err = json.NewDecoder(f).Decode(&bundle)
	if err != nil {
		return fmt.Errorf("decoding bundle.json: %w", err)
	}

	return c.ImportBundle(bundle, false)
}

func (c *CertManager) ImportAccount(af AccountFile, persist bool) error {
	c.accMu.Lock()
	defer c.accMu.Unlock()

	if c.hasAcc {
		return ErrAccountExists
	}

	pKey, err := pemToKey([]byte(af.PrivateKey))
	if err != nil {
		return fmt.Errorf("converting pem to pkey: %w", err)
	}
	c.account, err = c.client.UpdateAccount(acme.Account{
		PrivateKey: pKey,
		URL:        af.URL,
	}, c.config.Contact)

	if err != nil {
		return fmt.Errorf("reloading accounts: %w", err)
	}

	c.hasAcc = true
	c.accKey = pKey

	if persist {
		return c.persistAccount(af)
	}
	return nil
}

func (c *CertManager) ImportPrivateKey(keyPem string) error {
	c.certPKeyMu.Lock()
	defer c.certPKeyMu.Unlock()

	if c.hasCertPKey {
		return ErrPKeyExists
	}

	var err error
	c.certPKey, err = pemToKey([]byte(keyPem))
	if err != nil {
		return fmt.Errorf("decoding private key from pem: %w", err)
	}
	return nil
}

func (c *CertManager) ExportPrivateKey() ([]byte, error) {
	c.certPKeyMu.RLock()
	defer c.certPKeyMu.RUnlock()

	if !c.hasCertPKey {
		return nil, ErrNoPKey
	}

	pKey, err := keyToPEM(c.certPKey)
	if err != nil {
		return nil, fmt.Errorf("encoding private key to pem: %w", err)
	}
	return pKey, nil
}

func (c *CertManager) ImportBundle(bundle Bundle, persist bool) error {
	pKey, err := pemToKey([]byte(bundle.PrivateKey))
	if err != nil {
		return fmt.Errorf("decoding private key: %w", err)
	}
	cert, err := tls.X509KeyPair(
		[]byte(strings.Join(bundle.Chain, "\n")),
		[]byte(bundle.PrivateKey),
	)
	if err != nil {
		return fmt.Errorf("generating x509 key pair: %w", err)
	}
	c.certPKeyMu.Lock()
	c.certMu.Lock()
	defer c.certPKeyMu.Unlock()
	defer c.certMu.Unlock()

	c.certPKey = pKey
	c.storedCert = cert
	c.storedPem = bundle
	c.hasCert = true
	c.hasCertPKey = true

	if persist {
		return c.persisCerts(bundle)
	}

	return nil
}

func (c *CertManager) ExportBundle() (*Bundle, error) {
	c.certMu.RLock()
	defer c.certMu.RUnlock()
	if !c.hasCert {
		return nil, ErrNoCert
	}
	x := c.storedPem
	copy(x.Chain, c.storedPem.Chain)
	return &x, nil
}

func (c *CertManager) RequestCertificate() error {
	c.accMu.RLock()
	defer c.accMu.RUnlock()

	if !c.hasAcc {
		return ErrNoAccount
	}

	wildcard := false
	host := "_acme-challenge"
	common := c.config.RootZone
	apex := strings.TrimSuffix(c.config.Domain, c.config.RootZone)
	apex = strings.TrimSuffix(apex, ".")
	// TODO: the following routine is not robust
	switch {
	case apex == "":
	case apex[0] == 0x2a: // the "*" character
		wildcard = true
		host += string(apex[1:])
		common = string(apex[2:]) + "." + common
	default:
		host += "." + apex
		common = apex + "." + common
	}

	names := []string{common}
	if wildcard {
		names = append(names, c.config.Domain)
	}

	// generate private key if none and csr first
	var err error
	c.certPKeyMu.Lock()
	if c.certPKey == nil {
		c.certPKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			c.certPKeyMu.Unlock()
			return fmt.Errorf("generating certificate private key: %w", err)
		}
		c.hasCertPKey = true
	}

	tpl := &x509.CertificateRequest{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		PublicKeyAlgorithm: x509.ECDSA,
		PublicKey:          c.certPKey.Public(),
		Subject:            pkix.Name{CommonName: common},
		DNSNames:           names,
	}
	csrDer, err := x509.CreateCertificateRequest(rand.Reader, tpl, c.certPKey)
	c.certPKeyMu.Unlock()

	if err != nil {
		return fmt.Errorf("generating csr: %w", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDer)
	if err != nil {
		return fmt.Errorf("parsing csr: %w", err)
	}

	var pKey []byte
	pKey, err = c.ExportPrivateKey()
	if err != nil {
		return fmt.Errorf("encoding private key to pem: %w", err)
	}

	// now we can create a order
	o, err := c.client.NewOrderDomains(c.account, names...)
	if err != nil {
		return fmt.Errorf("creating order: %w", err)
	}

	for _, authURL := range o.Authorizations {
		auth, err := c.client.FetchAuthorization(c.account, authURL)
		if err != nil {
			return fmt.Errorf("fetching authoriztion: %w", err)
		}
		chal, ok := auth.ChallengeMap[acme.ChallengeTypeDNS01]
		if !ok {
			return errors.New("missing dns challenge")
		}

		txt := acme.EncodeDNS01KeyAuthorization(chal.KeyAuthorization)

		err = func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
			defer cancel()

			ok, err = c.config.DNSProvider.Update(ctx, host, "TXT", txt)
			if err != nil {
				return fmt.Errorf("updating dns record: %w", err)
			}
			if !ok {
				return errors.New("dns update failed")
			}

			chal, err = c.client.UpdateChallenge(c.account, chal)
			if err != nil {
				return fmt.Errorf("updating challenge: %w", err)
			}
			return nil
		}()
		if err != nil {
			return err
		}

		// TODO(zllovesuki): be smarter about checking for propagation
		<-time.After(time.Second * 15)
	}

	o, err = c.client.FinalizeOrder(c.account, o, csr)
	if err != nil {
		return fmt.Errorf("finalizing order: %w", err)
	}

	certs, err := c.client.FetchCertificates(c.account, o.Certificate)
	if err != nil {
		return fmt.Errorf("fetching certificates: %w", err)
	}

	var pemData []string
	for _, cert := range certs {
		pemData = append(pemData, strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: cert.Raw,
		}))))
	}

	bundle := Bundle{
		PrivateKey: string(pKey),
		Chain:      pemData,
	}

	if err := c.ImportBundle(bundle, true); err != nil {
		return fmt.Errorf("re-importing exported certificate: %w", err)
	}

	return nil
}

func (c *CertManager) GetCertificatesFunc(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.certMu.RLock()
	defer c.certMu.RUnlock()
	if !c.hasCert {
		return nil, errors.New("certificate not yet available")
	}
	return &c.storedCert, nil
}

func keyToPEM(pKey *ecdsa.PrivateKey) ([]byte, error) {
	enc, err := x509.MarshalECPrivateKey(pKey)
	if err != nil {
		return nil, fmt.Errorf("marshalling private key to pem: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: enc,
	}), nil
}

func pemToKey(b []byte) (*ecdsa.PrivateKey, error) {
	blk, _ := pem.Decode(b)
	pKey, err := x509.ParseECPrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key from pem: %w", err)
	}
	return pKey, nil
}
