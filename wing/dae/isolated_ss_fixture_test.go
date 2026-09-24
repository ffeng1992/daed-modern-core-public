//go:build linux && !dae_stub_ebpf

// SPDX-License-Identifier: AGPL-3.0-only
// Synthetic SS AEAD fixture adapted from github.com/olicesx/outbound
// cc86ced2e683 protocol/shadowsocks/e2e_test.go (AGPL-3.0).
// Wire framing is independent of the proxy client. No production secrets.
package dae

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"github.com/daeuniverse/outbound/ciphers"
	"github.com/daeuniverse/outbound/common"
	"golang.org/x/crypto/hkdf"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// readFullOrDead wraps io.ReadFull with the test deadline.
func readFullOrDead(t *testing.T, c net.Conn, buf []byte) error {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err := io.ReadFull(c, buf)
	return err
}

// ---- in-test shadowsocks AEAD server (independent wire-format implementation) ----

// aeadNonce is one shadowsocks AEAD nonce: the operation counter encoded
// little-endian. next() returns the nonce for the upcoming AEAD operation
// (starting at zero) and only then advances the counter, matching the client
// side contract of increment-after-use.
type aeadNonce struct {
	counter uint64
	size    int
}

func newAeadNonce(size int) *aeadNonce {
	return &aeadNonce{size: size}
}

func (n *aeadNonce) next() []byte {
	buf := make([]byte, n.size)
	for i := 0; i < 8 && i < n.size; i++ {
		buf[i] = byte(n.counter >> (8 * i))
	}
	n.counter++
	return buf
}

// deriveAeadSubkey computes the per-session subkey the way shadowsocks AEAD
// defines it. Implemented here with the raw HKDF primitive; the client's key
// schedule helpers are deliberately not used.
func deriveAeadSubkey(masterKey, salt []byte, keyLen int) ([]byte, error) {
	kdf := hkdf.New(sha1.New, masterKey, salt, []byte("ss-subkey"))
	subKey := make([]byte, keyLen)
	if _, err := io.ReadFull(kdf, subKey); err != nil {
		return nil, err
	}
	return subKey, nil
}

// aeadChunkReader turns the encrypted chunk stream into a plaintext
// io.Reader. It reassembles and verifies the [len][tag][payload][tag] chunks
// itself; a tag failure aborts the stream with an error.
type aeadChunkReader struct {
	t    *testing.T
	conn net.Conn
	aead interface {
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	}
	tagLen int
	nonce  *aeadNonce
	left   []byte
}

func (r *aeadChunkReader) Read(b []byte) (int, error) {
	if len(r.left) == 0 {
		hdr := make([]byte, 2+r.tagLen)
		if err := readFullOrDead(r.t, r.conn, hdr); err != nil {
			return 0, err
		}
		plainHdr, err := r.aead.Open(hdr[:0], r.nonce.next(), hdr, nil)
		if err != nil {
			return 0, err
		}
		length := int(binary.BigEndian.Uint16(plainHdr))
		frame := make([]byte, length+r.tagLen)
		if err := readFullOrDead(r.t, r.conn, frame); err != nil {
			return 0, err
		}
		payload, err := r.aead.Open(frame[:0], r.nonce.next(), frame, nil)
		if err != nil {
			return 0, err
		}
		r.left = payload
	}
	n := copy(b, r.left)
	r.left = r.left[n:]
	return n, nil
}

// aeadChunkWriter writes plaintext as shadowsocks AEAD chunks. On the first
// write it generates the reply salt, derives its own subkey and emits the
// salt ahead of the chunks.
type aeadChunkWriter struct {
	conn   net.Conn
	conf   *ciphers.CipherConf
	master []byte
	aead   interface {
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
	}
	nonce   *aeadNonce
	started bool
}

func (w *aeadChunkWriter) start() error {
	salt := make([]byte, w.conf.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	subKey, err := deriveAeadSubkey(w.master, salt, w.conf.KeyLen)
	if err != nil {
		return err
	}
	aead, err := w.conf.NewCipher(subKey)
	if err != nil {
		return err
	}
	w.aead = aead
	w.nonce = newAeadNonce(w.conf.NonceLen)
	if _, err := w.conn.Write(salt); err != nil {
		return err
	}
	w.started = true
	return nil
}

func (w *aeadChunkWriter) Write(b []byte) (int, error) {
	if !w.started {
		if err := w.start(); err != nil {
			return 0, err
		}
	}
	written := 0
	for written < len(b) {
		end := written + 16383
		if end > len(b) {
			end = len(b)
		}
		chunk := b[written:end]
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], uint16(len(chunk)))
		frame := make([]byte, 0, 2+w.conf.TagLen+len(chunk)+w.conf.TagLen)
		frame = w.aead.Seal(frame, w.nonce.next(), hdr[:], nil)
		frame = w.aead.Seal(frame, w.nonce.next(), chunk, nil)
		if err := w.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return written, err
		}
		if _, err := w.conn.Write(frame); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

// parseShadowsocksAddress decodes ATYP + address + port from the start of b
// and returns the dialable host:port plus the number of bytes consumed. This
// parser is written from the wire spec, not shared with the client.
func parseShadowsocksAddress(b []byte) (addr string, n int, err error) {
	if len(b) < 1 {
		return "", 0, io.ErrUnexpectedEOF
	}
	switch b[0] {
	case 1: // ipv4
		if len(b) < 7 {
			return "", 0, io.ErrUnexpectedEOF
		}
		return net.IP(b[1:5]).String() + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(b[5:7]))), 7, nil
	case 4: // ipv6
		if len(b) < 19 {
			return "", 0, io.ErrUnexpectedEOF
		}
		return net.IP(b[1:17]).String() + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(b[17:19]))), 19, nil
	case 3: // domain
		if len(b) < 2 {
			return "", 0, io.ErrUnexpectedEOF
		}
		l := int(b[1])
		if len(b) < 2+l+2 {
			return "", 0, io.ErrUnexpectedEOF
		}
		return string(b[2:2+l]) + ":" + strconv.Itoa(int(binary.BigEndian.Uint16(b[2+l:]))), 2 + l + 2, nil
	default:
		return "", 0, io.ErrUnexpectedEOF
	}
}

// parseShadowsocksUDPAddress decodes ATYP+ADDR+PORT from one decrypted UDP
// datagram and returns the address, the port and the payload offset.
func parseShadowsocksUDPAddress(b []byte) (ip net.IP, port int, offset int, err error) {
	addr, n, err := parseShadowsocksAddress(b)
	if err != nil {
		return nil, 0, 0, err
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, 0, 0, err
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return nil, 0, 0, io.ErrUnexpectedEOF
	}
	port, err = strconv.Atoi(portStr)
	if err != nil {
		return nil, 0, 0, err
	}
	return parsed, port, n, nil
}

// serveShadowsocksAeadConn handles one accepted connection as a classic
// shadowsocks AEAD server: it decrypts the chunk stream (own framing), parses
// the target address from the first chunk, relays to the real target with
// half-close propagation, and encrypts the replies with its own fresh salt.
func serveShadowsocksAeadConn(t *testing.T, downstream net.Conn, method, password string) {
	t.Helper()
	defer downstream.Close()
	conf, ok := ciphers.AeadCiphersConf[method]
	if !ok || conf.NewCipher == nil {
		t.Errorf("in-test server: unknown cipher %q", method)
		return
	}
	masterKey := common.EVPBytesToKey(password, conf.KeyLen)

	salt := make([]byte, conf.SaltLen)
	if err := readFullOrDead(t, downstream, salt); err != nil {
		return
	}
	subKey, err := deriveAeadSubkey(masterKey, salt, conf.KeyLen)
	if err != nil {
		return
	}
	aead, err := conf.NewCipher(subKey)
	if err != nil {
		return
	}
	reader := &aeadChunkReader{
		t:      t,
		conn:   downstream,
		aead:   aead,
		tagLen: conf.TagLen,
		nonce:  newAeadNonce(conf.NonceLen),
	}

	// The first chunk carries the target address in front of the payload.
	first := make([]byte, 4096)
	n, err := reader.Read(first)
	if err != nil {
		// An AEAD open failure here is exactly the wrong-password case: the
		// session is dead and the client must observe an error, not a hang.
		return
	}
	addr, addrLen, err := parseShadowsocksAddress(first[:n])
	if err != nil {
		return
	}
	reader.left = append(append([]byte(nil), first[addrLen:n]...), reader.left...)

	target, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return
	}
	defer target.Close()
	go func() {
		_, _ = io.Copy(target, reader)
		if tcp, ok := target.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	writer := &aeadChunkWriter{
		conn:   downstream,
		conf:   conf,
		master: masterKey,
	}
	_, _ = io.Copy(writer, target)
}

// serveShadowsocksAeadUDP is the UDP half of the in-test server: it decodes
// [salt][AEAD(ATYP+ADDR+PORT+payload)] per datagram (own framing), forwards
// the payload to the addressed target and encrypts the reply with a fresh
// salt plus the reply source address.
func serveShadowsocksAeadUDP(t *testing.T, pc net.PacketConn, method, password string, probes *atomic.Int64) {
	t.Helper()
	conf, ok := ciphers.AeadCiphersConf[method]
	if !ok || conf.NewCipher == nil {
		t.Errorf("in-test udp server: unknown cipher %q", method)
		return
	}
	masterKey := common.EVPBytesToKey(password, conf.KeyLen)
	buf := make([]byte, 65535)
	for {
		n, cli, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < conf.SaltLen+conf.TagLen {
			continue
		}
		salt := make([]byte, conf.SaltLen)
		copy(salt, buf[:conf.SaltLen])
		subKey, err := deriveAeadSubkey(masterKey, salt, conf.KeyLen)
		if err != nil {
			return
		}
		aead, err := conf.NewCipher(subKey)
		if err != nil {
			return
		}
		plain, err := aead.Open(buf[conf.SaltLen:conf.SaltLen],
			make([]byte, conf.NonceLen), buf[conf.SaltLen:n], nil)
		if err != nil {
			continue // undecryptable packet: drop, keep serving
		}
		ip, port, offset, err := parseShadowsocksUDPAddress(plain)
		if err != nil {
			continue
		}
		if string(plain[offset:]) == "bridge-probe" {
			probes.Add(1)
		}
		target, err := net.Dial("udp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
		if err != nil {
			continue
		}
		if _, err := target.Write(plain[offset:]); err != nil {
			target.Close()
			continue
		}
		_ = target.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp := make([]byte, 65535)
		m, err := target.Read(resp)
		target.Close()
		if err != nil {
			continue
		}
		// Reply plaintext: [ATYP ipv4/ipv6][source addr][port][payload].
		src := target.RemoteAddr().(*net.UDPAddr)
		var meta []byte
		if ip4 := src.IP.To4(); ip4 != nil {
			meta = append([]byte{1}, ip4...)
		} else {
			meta = append([]byte{4}, src.IP.To16()...)
		}
		var portBytes [2]byte
		binary.BigEndian.PutUint16(portBytes[:], uint16(src.Port))
		meta = append(meta, portBytes[:]...)
		plain = append(meta, resp[:m]...)

		reply := make([]byte, conf.SaltLen+len(plain)+conf.TagLen)
		replySalt := make([]byte, conf.SaltLen)
		if _, err := rand.Read(replySalt); err != nil {
			return
		}
		copy(reply, replySalt)
		replySubKey, err := deriveAeadSubkey(masterKey, replySalt, conf.KeyLen)
		if err != nil {
			return
		}
		replyAead, err := conf.NewCipher(replySubKey)
		if err != nil {
			return
		}
		sealed := replyAead.Seal(reply[conf.SaltLen:conf.SaltLen],
			make([]byte, conf.NonceLen), plain, nil)
		if _, err := pc.WriteTo(reply[:conf.SaltLen+len(sealed)], cli); err != nil {
			return
		}
	}
}
