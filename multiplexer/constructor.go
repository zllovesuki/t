package multiplexer

type Constructor func(Config) (Peer, error)

var protocolRegistry = map[Protocol]Constructor{}

func Register(p Protocol, c Constructor) {
	protocolRegistry[p] = c
}

func Get(p Protocol) (Constructor, error) {
	c, ok := protocolRegistry[p]
	if !ok || p == UnknownProtocol {
		return nil, ErrUnknownProtocol
	}
	return c, nil
}
