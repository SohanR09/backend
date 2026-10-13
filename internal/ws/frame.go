package ws

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

// WebSocket opcodes (RFC 6455 section 5.2).
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

// maxFramePayload bounds what we accept from a client. Clients only send
// control frames and tiny messages, so a small limit is plenty.
const maxFramePayload = 64 * 1024

var errFrameTooLarge = errors.New("websocket: frame too large")

// frame is a single decoded WebSocket frame.
type frame struct {
	fin     bool
	opcode  byte
	payload []byte
}

// readFrame reads and unmasks one client frame.
func readFrame(r *bufio.Reader) (frame, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return frame{}, err
	}
	f := frame{fin: hdr[0]&0x80 != 0, opcode: hdr[0] & 0x0F}
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return frame{}, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > maxFramePayload {
		return frame{}, errFrameTooLarge
	}
	// RFC 6455: frames from clients MUST be masked.
	if !masked {
		return frame{}, errors.New("websocket: client frame is not masked")
	}

	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return frame{}, err
	}
	f.payload = make([]byte, length)
	if _, err := io.ReadFull(r, f.payload); err != nil {
		return frame{}, err
	}
	for i := range f.payload {
		f.payload[i] ^= mask[i%4]
	}
	return f, nil
}

// appendFrame encodes an unmasked server frame with FIN set.
func appendFrame(dst []byte, opcode byte, payload []byte) []byte {
	dst = append(dst, 0x80|opcode)
	n := len(payload)
	switch {
	case n < 126:
		dst = append(dst, byte(n))
	case n <= 0xFFFF:
		dst = append(dst, 126, byte(n>>8), byte(n))
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		dst = append(dst, 127)
		dst = append(dst, ext[:]...)
	}
	return append(dst, payload...)
}
