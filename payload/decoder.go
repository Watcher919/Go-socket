package payload

import (
	"bufio"
	"io"
	"sync/atomic"

	"github.com/googollee/go-engine.io/base"
)

type readerArg struct {
	r   io.Reader
	typ base.FrameType
}

type decoder struct {
	errorChan  chan error
	readerChan chan readerArg
	lr         *limitReader
	closed     chan struct{}
	err        *atomic.Value
}

func newDecoder(closed chan struct{}, err *atomic.Value) *decoder {
	ret := &decoder{
		errorChan:  make(chan error),
		readerChan: make(chan readerArg),
		closed:     closed,
		err:        err,
	}
	ret.lr = newLimitReader(ret)
	return ret
}

func (r *decoder) NextReader() (base.FrameType, base.PacketType, io.ReadCloser, error) {
	arg, err := r.waitReader()
	if err != nil {
		return 0, 0, nil, err
	}

	br, ok := arg.r.(ByteReader)
	if !ok {
		br = bufio.NewReader(br)
	}
	var read func(br ByteReader) (base.FrameType, base.PacketType, io.ReadCloser, error)
	switch arg.typ {
	case base.FrameBinary:
		read = r.binaryRead
	case base.FrameString:
		read = r.stringRead
	default:
		return 0, 0, nil, ErrInvalidPayload
	}

	return read(br)
}

func (r *decoder) FeedIn(typ base.FrameType, rd io.Reader) error {
	select {
	case r.readerChan <- readerArg{
		r:   rd,
		typ: typ,
	}:
	case <-r.closed:
		return r.err.Load().(error)
	}
	select {
	case err := <-r.errorChan:
		return err
	case <-r.closed:
		return r.err.Load().(error)
	}
}

func (r *decoder) waitReader() (readerArg, error) {
	select {
	case ret := <-r.readerChan:
		return ret, nil
	case <-r.closed:
		return readerArg{}, r.err.Load().(error)
	}
}

func (r *decoder) closeFrame(err error) {
	select {
	case r.errorChan <- err:
	case <-r.closed:
	}
}

func (r *decoder) stringRead(br ByteReader) (base.FrameType, base.PacketType, io.ReadCloser, error) {
	l, err := readStringLen(br)
	if err != nil {
		return 0, 0, nil, err
	}

	ft := base.FrameString
	b, err := br.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	l--

	if b == 'b' {
		ft = base.FrameBinary
		b, err = br.ReadByte()
		if err != nil {
			return 0, 0, nil, err
		}
		l--
	}

	pt := base.ByteToPacketType(b, base.FrameString)
	r.lr.SetReader(br, l, ft == base.FrameBinary)
	return ft, pt, r.lr, nil
}

func (r *decoder) binaryRead(br ByteReader) (base.FrameType, base.PacketType, io.ReadCloser, error) {
	b, err := br.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	if b > 1 {
		return 0, 0, nil, ErrInvalidPayload
	}
	ft := base.ByteToFrameType(b)

	l, err := readBinaryLen(br)
	if err != nil {
		return 0, 0, nil, err
	}

	b, err = br.ReadByte()
	if err != nil {
		return 0, 0, nil, err
	}
	pt := base.ByteToPacketType(b, ft)
	l--

	r.lr.SetReader(br, l, false)
	return ft, pt, r.lr, nil
}
