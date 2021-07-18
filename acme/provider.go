package acme

import "context"

type Provider interface {
	Update(c context.Context, host string, t string, value string) (success bool, err error)
	Remove(c context.Context, host string, t string, value string) (success bool, err error)
}
