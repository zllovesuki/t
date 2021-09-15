// This package sets socket options per OS. Specifically:
// 1. It allows SO_REUSEADDR so we can bind on a more specific address
// 	when's there's another socket binding to wildcard;
// 2. It sets IP_PKTINFO on Linux/Windows, and IP_RECVDSTADDR on FreeBSD,
// 	so UDP protocols will get the DST IP (then becomes SRC IP) when
//	sending a return datagram. (https://blog.powerdns.com/2012/10/08/on-binding-datagram-udp-sockets-to-the-any-addresses/)
package sock
