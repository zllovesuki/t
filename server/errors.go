package server

import "fmt"

var (
	ErrDestinationNotFound = fmt.Errorf("destination not found among peers")
)
