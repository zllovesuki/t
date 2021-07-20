package multiplexer

import "fmt"

var (
	ErrDestinationNotFound = fmt.Errorf("destination not found among peers")
)
