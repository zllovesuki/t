package shared

import (
	"hash/fnv"
	"strings"

	"github.com/sethvargo/go-diceware/diceware"
)

func RandomHostname() string {
	g, err := diceware.Generate(5)
	if err != nil {
		return ""
	}
	return strings.Join(g, "-")
}

func PeerHash(name string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	return h.Sum64()
}
