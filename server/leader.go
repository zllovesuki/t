package server

import (
	"fmt"
	"sync/atomic"
	"time"
)

func (s *Server) handleMembershipChange() {
	go s.leaderTimer()
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-s.membershipCh:
			peers := append([]uint64{s.id}, s.peers.Snapshot()...)
			lowest := ^uint64(0)
			for _, peer := range peers {
				if peer < lowest {
					lowest = peer
				}
			}
			atomic.StoreUint64(s.currentLeader, lowest)
			fmt.Printf("calculated leader: %+v\n", lowest)
		}
	}
}

func (s *Server) leaderTimer() {
	for {
		select {
		case <-s.parentCtx.Done():
			return
		case <-time.After(time.Second * 30):
			if s.PeerID() != atomic.LoadUint64(s.currentLeader) {
				continue
			}
			fmt.Printf("Ha!\n")
		}
	}
}
