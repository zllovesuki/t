package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/zllovesuki/t/acme"

	"github.com/miekg/dns"
)

type RFC2136Config struct {
	TSIGKey    string
	TSIGAlgo   string
	TSIGSecret string
	Nameserver string
	Zone       string
}

type RFC2136 struct {
	conf       RFC2136Config
	nameserver string
	algo       string
	tsig       map[string]string
	zone       string
}

var _ acme.Provider = &RFC2136{}

func NewRFC2136Provider(conf RFC2136Config) (*RFC2136, error) {
	return &RFC2136{
		conf:       conf,
		nameserver: conf.Nameserver,
		zone:       dns.Fqdn(conf.Zone),
		algo:       dns.Fqdn(conf.TSIGAlgo),
		tsig: map[string]string{
			dns.Fqdn(conf.TSIGKey): conf.TSIGSecret,
		},
	}, nil
}

func (r *RFC2136) newClient() *dns.Client {
	client := &dns.Client{}
	client.SingleInflight = true
	client.TsigSecret = r.tsig
	return client
}

func (r *RFC2136) buildMsg(host string, t string, value string) (*dns.Msg, []dns.RR, error) {
	aRR, err := dns.NewRR(fmt.Sprintf("%s.%s %d IN %s %s", host, r.zone, 15, t, value))
	if err != nil {
		return nil, nil, err
	}
	rrs := []dns.RR{aRR}

	m := &dns.Msg{}
	m.SetUpdate(r.zone)
	m.SetTsig(r.conf.TSIGKey, r.conf.TSIGAlgo, 300, time.Now().Unix())

	return m, rrs, nil
}

func (r *RFC2136) Update(ctx context.Context, host string, t string, value string) (bool, error) {
	c := r.newClient()

	m, rrs, err := r.buildMsg(host, t, value)
	if err != nil {
		return false, err
	}

	m.RemoveRRset(rrs)
	m.Insert(rrs)

	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	in, _, err := c.ExchangeContext(ctx, m, r.nameserver)
	if err != nil {
		return false, err
	}

	return in.Rcode == dns.RcodeSuccess, nil
}

func (r *RFC2136) Remove(ctx context.Context, host string, t string, value string) (bool, error) {
	c := r.newClient()

	m, rrs, err := r.buildMsg(host, t, value)
	if err != nil {
		return false, err
	}

	m.Remove(rrs)

	ctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	in, _, err := c.ExchangeContext(ctx, m, r.nameserver)
	if err != nil {
		return false, err
	}

	return in.Rcode == dns.RcodeSuccess, nil
}
