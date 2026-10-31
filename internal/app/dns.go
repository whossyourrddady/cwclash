// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type DNSProxy struct {
	mu      sync.Mutex
	closers []io.Closer
	closed  bool
	limit   chan struct{}
	app     *App
}

func (a *App) startDNS(port int) (*DNSProxy, error) {
	d := &DNSProxy{limit: make(chan struct{}, 128), app: a}
	hosts := []string{"127.0.0.1"}
	if !a.dev {
		iface, err := net.InterfaceByName(a.snapshot().Settings.LANInterface)
		if err != nil {
			return nil, err
		}
		addresses, _ := iface.Addrs()
		for _, addr := range addresses {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.IsLoopback() {
				continue
			}
			host := ip.String()
			if ip.IsLinkLocalUnicast() && ip.To4() == nil {
				host += "%" + iface.Name
			}
			hosts = append(hosts, host)
		}
	}
	for _, host := range hosts {
		address := net.JoinHostPort(host, fmt.Sprint(port))
		udp, err := net.ListenPacket("udp", address)
		if err != nil {
			d.Close()
			return nil, fmt.Errorf("DNS UDP 监听失败：%w", err)
		}
		d.closers = append(d.closers, udp)
		go d.serveUDP(udp)
		tcp, err := net.Listen("tcp", address)
		if err != nil {
			d.Close()
			return nil, fmt.Errorf("DNS TCP 监听失败：%w", err)
		}
		d.closers = append(d.closers, tcp)
		go d.serveTCP(tcp)
	}
	return d, nil
}

func (d *DNSProxy) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	for _, c := range d.closers {
		_ = c.Close()
	}
}

func (d *DNSProxy) serveUDP(server net.PacketConn) {
	for {
		buf := make([]byte, 65535)
		n, client, err := server.ReadFrom(buf)
		if err != nil {
			return
		}
		query := buf[:n]
		if n < 12 || query[2]&0x80 != 0 {
			continue
		}
		select {
		case d.limit <- struct{}{}:
			go func() {
				defer func() { <-d.limit }()
				response := d.exchange("udp", client.String(), query)
				_, _ = server.WriteTo(response, client)
			}()
		default:
			_, _ = server.WriteTo(dnsFailure(query), client)
		}
	}
}

func (d *DNSProxy) serveTCP(server net.Listener) {
	for {
		client, err := server.Accept()
		if err != nil {
			return
		}
		select {
		case d.limit <- struct{}{}:
			go func() {
				defer func() { <-d.limit }()
				defer client.Close()
				for count := 0; count < 32; count++ {
					_ = client.SetDeadline(time.Now().Add(6 * time.Second))
					var size [2]byte
					if _, err := io.ReadFull(client, size[:]); err != nil {
						return
					}
					n := binary.BigEndian.Uint16(size[:])
					if n < 12 {
						return
					}
					query := make([]byte, n)
					if _, err := io.ReadFull(client, query); err != nil {
						return
					}
					response := d.exchange("tcp", client.RemoteAddr().String(), query)
					binary.BigEndian.PutUint16(size[:], uint16(len(response)))
					if _, err = client.Write(append(size[:], response...)); err != nil {
						return
					}
				}
			}()
		default:
			client.Close()
		}
	}
}

func (d *DNSProxy) exchange(network, client string, query []byte) []byte {
	start := time.Now()
	response := dnsFailure(query)
	connection, err := net.DialTimeout(network, "127.0.0.1:1053", 2*time.Second)
	if err == nil {
		_ = connection.SetDeadline(time.Now().Add(4 * time.Second))
		if network == "udp" {
			if _, err = connection.Write(query); err == nil {
				b := make([]byte, 65535)
				var n int
				n, err = connection.Read(b)
				if err == nil && n >= 12 && b[0] == query[0] && b[1] == query[1] {
					response = b[:n]
				}
			}
		} else {
			frame := make([]byte, 2+len(query))
			binary.BigEndian.PutUint16(frame[:2], uint16(len(query)))
			copy(frame[2:], query)
			if _, err = connection.Write(frame); err == nil {
				var size [2]byte
				if _, err = io.ReadFull(connection, size[:]); err == nil {
					b := make([]byte, binary.BigEndian.Uint16(size[:]))
					if _, err = io.ReadFull(connection, b); err == nil && len(b) >= 12 && b[0] == query[0] && b[1] == query[1] {
						response = b
					}
				}
			}
		}
		connection.Close()
	}
	name, typ := dnsQuestion(query)
	ip, _, _ := net.SplitHostPort(client)
	d.app.mu.Lock()
	d.app.sequence++
	d.app.dnsQueries = append(d.app.dnsQueries, DNSQuery{ID: d.app.sequence, Time: time.Now().Format(time.RFC3339), Client: ip, Name: name, Type: typ, RCODE: int(response[3] & 15), DurationMS: time.Since(start).Milliseconds()})
	if len(d.app.dnsQueries) > 1000 {
		d.app.dnsQueries = append([]DNSQuery(nil), d.app.dnsQueries[len(d.app.dnsQueries)-1000:]...)
	}
	d.app.mu.Unlock()
	return response
}

func dnsFailure(query []byte) []byte {
	if len(query) < 12 {
		return []byte{0, 0, 0x80, 2, 0, 0, 0, 0, 0, 0, 0, 0}
	}
	out := append([]byte(nil), query...)
	out[2] = (out[2] & 1) | 0x80
	out[3] = (out[3] & 0x10) | 0x82
	for i := 6; i < 12; i++ {
		out[i] = 0
	}
	return out
}

func dnsQuestion(packet []byte) (string, uint16) {
	if len(packet) < 12 {
		return "", 0
	}
	pos := 12
	labels := []string{}
	for steps := 0; steps < 128 && pos < len(packet); steps++ {
		n := int(packet[pos])
		pos++
		if n == 0 {
			if pos+4 <= len(packet) {
				return strings.Join(labels, "."), binary.BigEndian.Uint16(packet[pos : pos+2])
			}
			return strings.Join(labels, "."), 0
		}
		if n > 63 || pos+n > len(packet) {
			return "", 0
		}
		labels = append(labels, string(packet[pos:pos+n]))
		pos += n
	}
	return "", 0
}
