package mp3

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"strconv"
)

// Reader reads frames of an audio stream.
type Reader struct {
	reader *bufio.Reader
	offset int64
	err    error

	header FrameHeader
	data   []byte
}

// NewReader creates a new reader reading from r. The specified buffer size must
// fit an entire frame, and must fit the distance between the beginning of r and
// the first syncword.
func NewReader(r io.Reader, buffer int) *Reader {
	if buffer <= FrameHeaderSize {
		panic("mp3: invalid buffer size " + strconv.Itoa(buffer))
	}
	return &Reader{
		reader: bufio.NewReaderSize(r, buffer),
	}
}

// Reset clears the buffered data and error, replacing the underlying reader and
// the current offset. If offset is 0, the stream is resynchronized on the next
// call to Next.
func (r *Reader) Reset(x io.Reader, offset int64) {
	if offset < 0 {
		offset = 0
	}
	r.reader.Reset(x)
	r.offset = offset
	r.err = nil
	r.header = FrameHeader{}
	r.data = nil
}

// Validate causes the Reader to fail if the checksum for a frame is incorrect.
// TODO: func (r *Reader) ValidateChecksum()

// Err gets the current error. It is nil if no error occurred or the error is
// [io.EOF].
func (r *Reader) Err() error {
	if r.err != nil && r.err != io.EOF {
		return r.err
	}
	return nil
}

// Next reads a complete frame. It does not do anymore validation than strictly
// necessary. It must be called before the first frame is read. If an error
// occurs, false is returned.
//
// A frame starts with a syncword, and ends just before the next syncword. As
// such, the reader offset minus the length of the raw frame is the offset of
// the start of the frame, and the total length of all raw frames plus the
// offset of the first syncword equals the length of the stream.
func (r *Reader) Next() bool {
	if r.err != nil {
		return false
	}
	r.err = r.next()
	return r.err == nil
}

func (r *Reader) next() error {
	if r.offset == 0 {
		buf, err := r.reader.Peek(r.reader.Size())
		if err != nil && err != io.EOF {
			return err
		}
		i := Sync(buf)
		if i == -1 {
			return ErrUnsynchronized
		}
		n, err := r.reader.Discard(i)
		r.offset += int64(n)
		if err != nil {
			return err
		}
	}

	buf, err := r.reader.Peek(FrameHeaderSize)
	if err != nil {
		return err
	}
	r.header.decode(buf)

	switch r.header.ID {
	case MPEGVersion1, MPEGVersion2, MPEGVersion2_5:
	default:
		return errors.New("invalid mpeg version")
	}

	switch r.header.Layer {
	case MPEGLayerI, MPEGLayerII, MPEGLayerIII:
	default:
		return errors.New("invalid mpeg layer")
	}

	if _, ok := r.header.Bitrate(); !ok {
		return errors.New("invalid bitrate index")
	}
	if _, ok := r.header.SamplingFrequency(); !ok {
		return errors.New("invalid sampling frequency index")
	}

	var slots int
	if r.header.BitrateIndex == BitrateIndexFree {
		return errors.New("free bitrate index not implemented yet") // TODO
	} else {
		var ok bool
		slots, _, ok = r.header.Slots()
		if !ok {
			panic("wtf") // this should never fail if the checks above passed
		}
	}

	slotSize, ok := r.header.SlotSize()
	if !ok {
		panic("wtf") // this should never fail if the checks above passed
	}

	bytes := slots * slotSize
	if r.header.Padding {
		bytes += slotSize
	}
	if bytes < FrameHeaderSize {
		panic("wtf") // this should never fail if the checks above passed
	}

	// we use Peek instead of ReadFull to ensure no more than the configured
	// buffer size is read
	buf, err = r.reader.Peek(bytes)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return err
	}
	r.data = buf

	n, err := r.reader.Discard(bytes)
	r.offset += int64(n)
	if err != nil {
		return err
	}

	return nil
}

// Offset gets the offset of the end of the current frame (i.e., the start of
// the next frame).
func (r *Reader) Offset() int64 {
	return r.offset
}

// FrameHeader returns the current frame header. It may be overwritten on the
// next call to Next.
func (r *Reader) Header() *FrameHeader {
	return &r.header
}

// Raw returns the raw frame data including the header. It may be overwritten on
// the next call to Next.
func (r *Reader) Raw() []byte {
	return r.data
}

// ErrorCheck returns the 16 bit parity-check word used for optional error
// detection. If the protection flag in the header is not set, false is
// returned.
func (r *Reader) ErrorCheck() (uint16, bool) {
	if !r.header.Protection {
		return 0, false
	}
	return binary.BigEndian.Uint16(r.data[FrameHeaderSize : FrameHeaderSize+2]), true
}

// TODO: func (r *Reader) Padding() ([]byte, bool)
// TODO: func (r *Reader) Data() []byte
