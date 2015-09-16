// Copyright 2015 someonegg. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bdmsg

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/someonegg/goutility/poolutil"
	"io"
	"sync"
)

var (
	ErrMsgTooBig = errors.New("message is too big")
	ErrMsgPacket = errors.New("message packet is wrong")
	ErrPackMsg   = errors.New("pack message failed")
)

// Message is a variable-length byte array.
type Msg []byte

// Message is identified by id.
type MsgId int32

type MsgReader interface {
	ReadMsg() (id MsgId, m Msg, err error)
}

type MsgWriter interface {
	WriteMsg(id MsgId, m Msg) (err error)
}

type MsgReadWriter interface {
	MsgReader
	MsgWriter
}

/*
In the transport layer, message's layout is:

	A(length) + B(id) + C(data)
	A is 4-bytes int, big-endian
	B is 4-bytes int, big-endian
	C is byte array, its length is (A) - 4
*/
type MsgRWIO struct {
	RW     io.ReadWriter
	MsgMax int
}

func NewMsgRWIO(rw io.ReadWriter, msgMax int) *MsgRWIO {
	return &MsgRWIO{RW: rw, MsgMax: msgMax}
}

func (rw *MsgRWIO) ReadMsg() (id MsgId, m Msg, err error) {
	var _l int32
	err = binary.Read(rw.RW, binary.BigEndian, &_l)
	if err != nil {
		return
	}
	l := int(_l)

	if l < 4 {
		err = ErrMsgPacket
		return
	}
	l -= 4
	if l > rw.MsgMax {
		err = ErrMsgTooBig
		return
	}

	var _id int32
	err = binary.Read(rw.RW, binary.BigEndian, &_id)
	if err != nil {
		return
	}
	id = MsgId(_id)

	m = poolutil.BufGet(l)
	readed := 0
	for readed < l {
		var n int
		n, err = rw.RW.Read(m[readed:])
		if n > 0 {
			readed += n
			continue
		}
		if err != nil {
			return
		}
	}

	return id, m, nil
}

func (rw *MsgRWIO) WriteMsg(id MsgId, m Msg) (err error) {
	l := len(m)
	if l > rw.MsgMax {
		err = ErrMsgTooBig
		return
	}
	l += 4

	err = binary.Write(rw.RW, binary.BigEndian, int32(l))
	if err != nil {
		return
	}

	err = binary.Write(rw.RW, binary.BigEndian, int32(id))
	if err != nil {
		return
	}

	n, err := rw.RW.Write(m)
	if err != nil {
		return
	}
	if (n + 4) != l {
		// Write must return a non-nil error if it returns n < len(p)
		err = ErrPackMsg
		return
	}

	return nil
}

type Converter interface {
	Convert(rw io.ReadWriter) MsgReadWriter
}

// The default maximum message length is 128K.
const DefaultMaxMsg = 128 * 1024

type DefaultConverter struct {
	MsgMax int
}

func (c *DefaultConverter) Convert(rw io.ReadWriter) MsgReadWriter {
	return NewMsgRWIO(rw, c.MsgMax)
}

// The default io.ReadWriter to MsgReadWriter converter.
var DefaultIOC = Converter(&DefaultConverter{MsgMax: DefaultMaxMsg})

/*
The dump format is:

	R|W\nMessageId\nMessageSize\nMessageData\n\n
	R for read, W for write
	MessageData part is raw data
*/
type MsgRWDump struct {
	rw        MsgReadWriter
	ifDiscard func(MsgId, Msg) bool
	dL        sync.Mutex
	dump      io.ReadWriteCloser
}

func NewMsgRWDump(rw MsgReadWriter, ifDiscard func(MsgId,
	Msg) bool) *MsgRWDump {

	return &MsgRWDump{rw: rw, ifDiscard: ifDiscard}
}

func (rw *MsgRWDump) SetDump(dump io.ReadWriteCloser) io.ReadWriteCloser {
	rw.dL.Lock()
	defer rw.dL.Unlock()
	od := rw.dump
	rw.dump = dump
	return od
}

func (rw *MsgRWDump) Dump() io.ReadWriteCloser {
	rw.dL.Lock()
	defer rw.dL.Unlock()
	return rw.dump
}

func (rw *MsgRWDump) OnStop() {
	rw.dL.Lock()
	defer rw.dL.Unlock()
	if rw.dump != nil {
		rw.dump.Close()
		rw.dump = nil
	}
}

// Not support concurrently access.
func (rw *MsgRWDump) ReadMsg() (id MsgId, m Msg, err error) {
	id, m, err = rw.rw.ReadMsg()
	if err != nil {
		return
	}

	if rw.ifDiscard != nil && rw.ifDiscard(id, m) {
		return
	}

	rw.dL.Lock()
	defer rw.dL.Unlock()

	d := rw.dump
	if d == nil {
		return
	}

	fmt.Fprintf(d, "R %v %v\n", id, len(m))
	d.Write(m)
	fmt.Fprintf(d, "\n\n")

	return
}

// Not support concurrently access.
func (rw *MsgRWDump) WriteMsg(id MsgId, m Msg) (err error) {
	err = rw.rw.WriteMsg(id, m)
	if err != nil {
		return
	}

	if rw.ifDiscard != nil && rw.ifDiscard(id, m) {
		return
	}

	rw.dL.Lock()
	defer rw.dL.Unlock()

	d := rw.dump
	if d == nil {
		return
	}

	fmt.Fprintf(d, "W %v %v\n", id, len(m))
	d.Write(m)
	fmt.Fprintf(d, "\n\n")

	return
}