package minecraft

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// PacketData holds the data of a Minecraft packet.
type PacketData struct {
	Header  *packet.Header
	Full    []byte
	Payload *bytes.Buffer
}

func (p *PacketData) DecodePool(pool *packet.Pool) (pks packet.Packet, err error) {
	pkFunc, ok := (*pool)[p.Header.PacketID]
	var pk packet.Packet
	if !ok {
		pk = &packet.Unknown{PacketID: p.Header.PacketID}
	} else {
		pk = pkFunc()
	}

	defer func() {
		if recoveredErr := recover(); recoveredErr != nil {
			err = fmt.Errorf("decode packet %T: %w", pk, recoveredErr.(error))
		}
	}()

	r := DefaultProtocol.NewReader(p.Payload, 0, true)
	pk.Marshal(r)
	if p.Payload.Len() != 0 {
		err = fmt.Errorf("decode packet %T: %v unread bytes left: 0x%x", pk, p.Payload.Len(), p.Payload.Bytes())
	}
	return pk, err
}

// ParseData parses the packet data slice passed into a PacketData struct.
func ParseData(data []byte, conn *Conn) (*PacketData, error) {
	buf := bytes.NewBuffer(data)
	header := &packet.Header{}
	if err := header.Read(buf); err != nil {
		// We don't return this as an error as it's not in the hand of the user to control this. Instead,
		// we return to reading a new packet.
		return nil, fmt.Errorf("read packet header: %w", err)
	}
	if conn != nil && conn.packetFunc != nil {
		// The packet func was set, so we call it.
		conn.packetFunc(*header, buf.Bytes(), conn.RemoteAddr(), conn.LocalAddr())
	}
	return &PacketData{Header: header, Full: data, Payload: buf}, nil
}

type unknownPacketError struct {
	id uint32
}

func (err unknownPacketError) Error() string {
	return fmt.Sprintf("unexpected packet (ID=%v)", err.id)
}

// Decode decodes the packet Payload held in the PacketData and returns the packet.Packet decoded.
func (p *PacketData) Decode(conn *Conn) (pks []packet.Packet, err error) {
	// Attempt to fetch the packet with the right packet ID from the pool.
	pkFunc, ok := conn.pool[p.Header.PacketID]
	var pk packet.Packet
	if !ok {
		// No packet with the ID. This may be a custom packet of some sorts.
		pk = &packet.Unknown{PacketID: p.Header.PacketID}
		if conn.disconnectOnUnknownPacket {
			_ = conn.Close()
			return nil, unknownPacketError{id: p.Header.PacketID}
		}
	} else {
		pk = pkFunc()
	}

	defer func() {
		if recoveredErr := recover(); recoveredErr != nil {
			err = fmt.Errorf("decode packet %T: %w", pk, recoveredErr.(error))
		}
		if err != nil && !errors.Is(err, unknownPacketError{}) && conn.disconnectOnInvalidPacket {
			_ = conn.Close()
		}
	}()

	r := conn.proto.NewReader(p.Payload, conn.shieldID.Load(), conn.readerLimits)
	pk.Marshal(r)
	if p.Payload.Len() != 0 {
		err = fmt.Errorf("decode packet %T: %v unread bytes left: 0x%x", pk, p.Payload.Len(), p.Payload.Bytes())
	}
	if conn.disconnectOnInvalidPacket && err != nil {
		return nil, err
	}
	return conn.proto.ConvertToLatest(pk, conn), err
}
