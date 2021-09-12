package peer

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"github.com/zllovesuki/t/multiplexer"
)

type DialOptions struct {
	Protocol multiplexer.Protocol
	Addr     string
	TLS      *tls.Config
}

func Dial(d DialOptions) (connector interface{}, hs net.Conn, closer func(), err error) {
	switch d.Protocol {
	case multiplexer.QUICProtocol:
		var sess quic.Session
		sess, err = quic.DialAddr(d.Addr, d.TLS.Clone(), QUICConfig())
		if err != nil {
			err = errors.Wrap(err, "opening quic session")
			return
		}
		closer = func() {
			sess.CloseWithError(quic.ApplicationErrorCode(0), "")
		}
		conn, sErr := sess.OpenStream()
		if sErr != nil {
			err = errors.Wrap(sErr, "opening quic handshake connection")
			return
		}
		connector = sess
		hs = WrapQUIC(sess, conn)
	case multiplexer.MplexProtocol, multiplexer.YamuxProtocol:
		conn, sErr := tls.DialWithDialer(&net.Dialer{
			Timeout: time.Second * 3,
		}, "tcp", d.Addr, d.TLS.Clone())
		if sErr != nil {
			err = errors.Wrap(sErr, "opening tls connection")
			return
		}
		closer = func() {
			conn.Close()
		}
		connector = conn
		hs = conn
	default:
		err = errors.New("unknown protocol")
		return
	}

	return
}
