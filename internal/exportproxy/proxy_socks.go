package exportproxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

func serveSOCKS(client net.Conn, config Config, dialer *net.Dialer, resolver *net.Resolver) error {
	reader := bufio.NewReader(client)
	version, err := reader.ReadByte()
	if err != nil || version != 5 {
		return errors.New("unsupported SOCKS version")
	}
	methodCount, err := reader.ReadByte()
	if err != nil {
		return err
	}
	methods := make([]byte, methodCount)
	if _, err := io.ReadFull(reader, methods); err != nil {
		return err
	}
	chosen := byte(0xff)
	if config.AuthEnabled && hasMethod(methods, 2) {
		chosen = 2
	} else if !config.AuthEnabled && hasMethod(methods, 0) {
		chosen = 0
	}
	if _, err := client.Write([]byte{5, chosen}); err != nil || chosen == 0xff {
		return errors.New("no acceptable SOCKS authentication method")
	}
	if chosen == 2 {
		if err := socksAuthenticate(reader, client, config); err != nil {
			return err
		}
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return err
	}
	if header[0] != 5 {
		_ = writeSocksReply(client, 1)
		return errors.New("invalid SOCKS5 request version")
	}
	switch header[1] {
	case 1:
		return serveSOCKSConnect(client, reader, header[3], dialer, resolver)
	case 3:
		return serveSOCKSUDPAssociate(client, reader, header[3], dialer)
	default:
		_ = writeSocksReply(client, 7)
		return fmt.Errorf("unsupported SOCKS5 command %d", header[1])
	}
}

func serveSOCKSConnect(client net.Conn, reader *bufio.Reader, addressType byte, dialer *net.Dialer, resolver *net.Resolver) error {
	host, port, err := readSocksAddress(reader, addressType)
	if err != nil {
		_ = writeSocksReply(client, 1)
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), proxyTimeout)
	target, err := dialTarget(ctx, net.JoinHostPort(host, strconv.Itoa(port)), dialer, resolver)
	cancel()
	if err != nil {
		_ = writeSocksReply(client, 5)
		return err
	}
	defer target.Close()
	if err := writeSocksReply(client, 0); err != nil {
		return err
	}
	if buffered := reader.Buffered(); buffered > 0 {
		if value, err := reader.Peek(buffered); err == nil {
			_, _ = target.Write(value)
			_, _ = reader.Discard(buffered)
		}
	}
	pipe(client, target)
	return nil
}

func serveSOCKSUDPAssociate(client net.Conn, reader *bufio.Reader, addressType byte, dialer *net.Dialer) error {
	_, _, err := readSocksAddress(reader, addressType)
	if err != nil {
		_ = writeSocksReply(client, 1)
		return err
	}
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		_ = writeSocksReply(client, 1)
		return fmt.Errorf("listen UDP: %w", err)
	}
	localAddr := udpConn.LocalAddr().(*net.UDPAddr)
	if err := writeUDPAssociateReply(client, localAddr); err != nil {
		udpConn.Close()
		return err
	}
	relay := &udpRelay{
		conn:   udpConn,
		dialer: dialer,
		done:   make(chan struct{}),
	}
	go relay.serve()
	_, _ = io.Copy(io.Discard, client)
	relay.close()
	return nil
}

type udpRelay struct {
	conn       *net.UDPConn
	dialer     *net.Dialer
	clientAddr atomic.Pointer[net.UDPAddr]
	done       chan struct{}
	closeOnce  sync.Once
}

func (r *udpRelay) serve() {
	defer close(r.done)
	buf := make([]byte, 65535)
	for {
		n, addr, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if r.clientAddr.Load() == nil {
			r.clientAddr.Store(addr)
		}
		go r.handlePacket(buf[:n], addr)
	}
}

func (r *udpRelay) handlePacket(data []byte, from *net.UDPAddr) {
	if len(data) < 4 || data[0] != 0 || data[1] != 0 || data[2] != 0 {
		return
	}
	targetHost, targetPort, payloadOffset, err := parseUDPHeader(data)
	if err != nil || payloadOffset >= len(data) {
		return
	}
	payload := data[payloadOffset:]
	ctx, cancel := context.WithTimeout(context.Background(), proxyTimeout)
	defer cancel()
	targetAddr := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	targetConn, err := r.dialer.DialContext(ctx, "udp", targetAddr)
	if err != nil {
		return
	}
	defer targetConn.Close()
	_ = targetConn.SetDeadline(time.Now().Add(proxyTimeout))
	if _, err := targetConn.Write(payload); err != nil {
		return
	}
	responseBuf := make([]byte, 65535)
	n, err := targetConn.Read(responseBuf)
	if err != nil || n == 0 {
		return
	}
	reply := buildUDPReply(targetHost, targetPort, responseBuf[:n])
	_, _ = r.conn.WriteToUDP(reply, from)
}

func (r *udpRelay) close() {
	r.closeOnce.Do(func() {
		_ = r.conn.Close()
	})
}

func parseUDPHeader(data []byte) (host string, port int, payloadOffset int, err error) {
	if len(data) < 4 {
		return "", 0, 0, errors.New("UDP header too short")
	}
	offset := 3
	switch data[offset] {
	case 1:
		if len(data) < offset+1+4+2 {
			return "", 0, 0, errors.New("truncated IPv4 UDP header")
		}
		offset++
		host = net.IP(data[offset : offset+4]).String()
		offset += 4
	case 3:
		if len(data) < offset+2 {
			return "", 0, 0, errors.New("truncated domain UDP header")
		}
		offset++
		domainLen := int(data[offset])
		offset++
		if len(data) < offset+domainLen+2 {
			return "", 0, 0, errors.New("truncated domain in UDP header")
		}
		host = string(data[offset : offset+domainLen])
		offset += domainLen
	case 4:
		if len(data) < offset+1+16+2 {
			return "", 0, 0, errors.New("truncated IPv6 UDP header")
		}
		offset++
		host = net.IP(data[offset : offset+16]).String()
		offset += 16
	default:
		return "", 0, 0, fmt.Errorf("unsupported UDP address type %d", data[offset])
	}
	if len(data) < offset+2 {
		return "", 0, 0, errors.New("truncated port in UDP header")
	}
	port = int(binary.BigEndian.Uint16(data[offset : offset+2]))
	offset += 2
	return host, port, offset, nil
}

func buildUDPReply(targetHost string, targetPort int, payload []byte) []byte {
	ip := net.ParseIP(targetHost)
	var reply []byte
	reply = append(reply, 0, 0, 0)
	if ipv4 := ip.To4(); ipv4 != nil {
		reply = append(reply, 1)
		reply = append(reply, ipv4...)
	} else if ipv6 := ip.To16(); ipv6 != nil {
		reply = append(reply, 4)
		reply = append(reply, ipv6...)
	} else {
		reply = append(reply, 3)
		reply = append(reply, byte(len(targetHost)))
		reply = append(reply, []byte(targetHost)...)
	}
	reply = append(reply, byte(targetPort>>8), byte(targetPort))
	reply = append(reply, payload...)
	return reply
}

func writeUDPAssociateReply(conn net.Conn, addr *net.UDPAddr) error {
	reply := []byte{5, 0, 0, 1}
	if ipv4 := addr.IP.To4(); ipv4 != nil {
		reply = append(reply, ipv4...)
	} else {
		reply = append(reply, 0, 0, 0, 0)
	}
	reply = append(reply, byte(addr.Port>>8), byte(addr.Port))
	_, err := conn.Write(reply)
	return err
}

func socksAuthenticate(reader *bufio.Reader, writer io.Writer, config Config) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != 1 {
		return errors.New("invalid SOCKS authentication request")
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, username); err != nil {
		return err
	}
	length, err := reader.ReadByte()
	if err != nil {
		return err
	}
	password := make([]byte, int(length))
	if _, err := io.ReadFull(reader, password); err != nil {
		return err
	}
	if string(username) != config.Username || string(password) != config.Password {
		_, _ = writer.Write([]byte{1, 1})
		return errors.New("SOCKS authentication failed")
	}
	_, err = writer.Write([]byte{1, 0})
	return err
}

func hasMethod(methods []byte, wanted byte) bool {
	for _, method := range methods {
		if method == wanted {
			return true
		}
	}
	return false
}

func readSocksAddress(reader *bufio.Reader, kind byte) (string, int, error) {
	var host string
	switch kind {
	case 1:
		value := make([]byte, 4)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = net.IP(value).String()
	case 3:
		length, err := reader.ReadByte()
		if err != nil {
			return "", 0, err
		}
		value := make([]byte, int(length))
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = string(value)
	case 4:
		value := make([]byte, 16)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = net.IP(value).String()
	default:
		return "", 0, fmt.Errorf("unsupported SOCKS address type %d", kind)
	}
	value := make([]byte, 2)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", 0, err
	}
	return host, int(binary.BigEndian.Uint16(value)), nil
}

func writeSocksReply(connection net.Conn, code byte) error {
	_, err := connection.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
	return err
}
