package app

import "context"

type Provider interface {
	Update(c context.Context, host string, ip string) (success bool, err error)
	Remove(c context.Context, host string, ip string) (success bool, err error)
}
