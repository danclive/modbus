// Copyright 2014 Quoc-Viet Nguyen. All rights reserved.
// This software may be modified and distributed under the terms
// of the BSD license.  See the LICENSE file for details.
package modbus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"
)

const (
	TcpProtocolIdentifier uint16 = 0x0000
	TcpUnitIdentifier     byte   = 0xFF

	// Modbus Application Protocol
	TcpHeaderLength = 7
	TcpMaxADULength = 260
)

type TcpClientHandler struct {
	TcpEncodeDecoder
	TcpTransporter
}

func TcpClient(address string) Client {
	handler := &TcpClientHandler{}
	handler.Address = address
	return TcpClientWithHandler(handler)
}

func TcpClientWithHandler(handler *TcpClientHandler) Client {
	return &client{encoder: handler, decoder: handler, transporter: handler}
}

// Implements Encoder and Decoder interface
type TcpEncodeDecoder struct {
	// For synchronization between messages of server & client
	// TODO put in a context for the sake of thread-safe
	transactionId uint16
	unitId        byte
}

// Adds modbus application protocol header:
//  Transaction identifier: 2 bytes
//  Protocol identifier: 2 bytes
//  Length: 2 bytes
//  Unit identifier: 1 byte
func (mb *TcpEncodeDecoder) Encode(pdu *ProtocolDataUnit) (adu []byte, err error) {
	var buf bytes.Buffer

	// Transaction identifier
	mb.transactionId++
	if err = binary.Write(&buf, binary.BigEndian, mb.transactionId); err != nil {
		return
	}
	// Protocol identifier
	if err = binary.Write(&buf, binary.BigEndian, TcpProtocolIdentifier); err != nil {
		return
	}
	// Length = sizeof(UnitId) + sizeof(FunctionCode) + Data
	length := uint16(1 + 1 + len(pdu.Data))
	if err = binary.Write(&buf, binary.BigEndian, length); err != nil {
		return
	}
	// Unit identifier
	if err = binary.Write(&buf, binary.BigEndian, mb.unitId); err != nil {
		return
	}
	// PDU
	var n int
	if err = buf.WriteByte(pdu.FunctionCode); err != nil {
		return
	}
	if n, err = buf.Write(pdu.Data); err != nil {
		return
	}
	if n != len(pdu.Data) {
		err = fmt.Errorf("modbus: encoded pdu size '%v' does not match expected '%v'", len(pdu.Data), n)
		return
	}
	adu = buf.Bytes()
	return
}

func (mb *TcpEncodeDecoder) Decode(adu []byte) (pdu *ProtocolDataUnit, err error) {
	var (
		transactionId uint16
		protocolId    uint16
		length        uint16
		unitId        uint8
	)

	buf := bytes.NewReader(adu)
	if err = binary.Read(buf, binary.BigEndian, &transactionId); err != nil {
		return
	}
	// Not thread safe yet
	if transactionId != mb.transactionId {
		err = fmt.Errorf("modbus: adu transaction id '%v' does not match request '%v'", transactionId, mb.transactionId)
		return
	}
	if err = binary.Read(buf, binary.BigEndian, &protocolId); err != nil {
		return
	}
	if protocolId != TcpProtocolIdentifier {
		err = fmt.Errorf("modbus: adu protocol id '%v' does not match request '%v'", protocolId, TcpProtocolIdentifier)
		return
	}
	if err = binary.Read(buf, binary.BigEndian, &length); err != nil {
		return
	}
	if err = binary.Read(buf, binary.BigEndian, &unitId); err != nil {
		return
	}
	if unitId != mb.unitId {
		err = fmt.Errorf("modbus: adu unit id '%v' does not match request '%v'", unitId, mb.unitId)
		return
	}
	pduLength := buf.Len()
	if pduLength == 0 || pduLength != int(length-1) {
		err = fmt.Errorf("modbus: adu length '%v' does not match pdu data '%v'", length-1, pduLength)
		return
	}
	pdu = &ProtocolDataUnit{}
	if err = binary.Read(buf, binary.BigEndian, &pdu.FunctionCode); err != nil {
		return
	}
	pdu.Data = make([]byte, pduLength-1)
	var n int
	if n, err = buf.Read(pdu.Data); err != nil {
		return
	}
	if n != pduLength-1 {
		err = fmt.Errorf("modbus: pdu data size '%v' does not match expected '%v'", n, pduLength-1)
		return
	}
	return
}

// Implements Transporter interface
type TcpTransporter struct {
	Address string
	Timeout time.Duration
	Logger  *log.Logger
}

func (mb *TcpTransporter) Send(aduRequest []byte) (aduResponse []byte, err error) {
	dialer := net.Dialer{Timeout: mb.Timeout}
	conn, err := dialer.Dial("tcp", mb.Address)
	if err != nil {
		return
	}
	defer conn.Close()

	if mb.Logger != nil {
		mb.Logger.Printf("modbus: sending %v\n", aduRequest)
	}
	var n int
	if n, err = conn.Write(aduRequest); err != nil {
		return
	}
	if n != len(aduRequest) {
		err = fmt.Errorf("modbus: sent adu length '%v' does not match expected '%v'", n, len(aduRequest))
		// TODO: flush
		return
	}
	// Read header first
	data := [TcpMaxADULength]byte{}
	if n, err = conn.Read(data[:TcpHeaderLength]); err != nil {
		return
	}
	if mb.Logger != nil {
		mb.Logger.Printf("modbus: received header %v\n", data[:TcpHeaderLength])
	}
	if n != TcpHeaderLength {
		err = fmt.Errorf("modbus: response header length '%v' does not match expected '%v'", n, TcpHeaderLength)
		return
	}
	// Read length, ignore transaction & protocol id (4 bytes)
	length := int(binary.BigEndian.Uint16(data[4:]))
	if length <= 0 {
		err = fmt.Errorf("modbus: response header length '%v' must not be zero", length)
		return
	}
	// Skip unit id
	length = TcpHeaderLength - 1 + length
	idx := TcpHeaderLength
	for idx < length {
		if n, err = conn.Read(data[idx:length]); err != nil {
			return
		}
		idx += n
	}
	aduResponse = data[:idx]
	if mb.Logger != nil {
		mb.Logger.Printf("modbus: received %v\n", aduResponse)
	}
	return
}
