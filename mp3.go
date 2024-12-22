// Package mp3 implements the [ISO/IEC 11172-3:1993] bitstream with support for
// the extensions in [ISO/IEC 13818-3:1998] section 2.4.1.
//
// [ISO/IEC 11172-3:1993]: https://www.iso.org/standard/22412.html
// [ISO/IEC 13818-3:1998]: https://www.iso.org/standard/26797.html
package mp3

// links to copies of draft standards:
//  - https://csclub.uwaterloo.ca/~pbarfuss/ISO11172-3.pdf
//  - https://ossrs.io/lts/zh-cn/assets/files/ISO_IEC_13818-3-MP3-1997-8bbd47f7cd4e0325f23b9473f6932fa1.pdf

import (
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
)

var ErrUnsynchronized = errors.New("no syncword found")

type MPEGVersion uint8 // 2 bits

const (
	MPEGVersion2_5 MPEGVersion = iota
	MPEGVersionReserved
	MPEGVersion2
	MPEGVersion1
)

type MPEGLayer uint8 // 2 bits

const (
	MPEGLayerReserved MPEGLayer = iota
	MPEGLayerIII
	MPEGLayerII
	MPEGLayerI
)

type Mode uint8 // 2 bits

const (
	ModeStereo Mode = iota
	ModeJointStereo
	ModeDualChannel
	ModeSingleChannel
)

type ModeExtension uint8 // 2 bits

// Bound is the value of [ModeExtension] for [MPEGLayerI] and [MPEGLayerII]. It
// indicates which subbands are in intensity_stereo when [ModeJointStereo] is
// used.
func (m ModeExtension) Bound() (int, bool) {
	if m > 0b11 {
		return -1, false
	}
	return [...]int{4, 8, 12, 16}[m], true
}

// Coding is the value of [ModeExtension] for [MPEGLayerIII]. It indicates which
// type of joint stereo coding is applied. The frequency ranges are implicit. If
// both intensityStereo and msStereo are false, the mode is effectively the same
// as [ModeStereo].
func (m ModeExtension) Coding() (intensityStereo bool, msStereo bool, ok bool) {
	if m <= 0b11 {
		intensityStereo = [...]bool{false, true, false, true}[m]
		msStereo = [...]bool{false, false, true, true}[m]
		ok = true
	}
	return
}

type Emphasis uint8 // 2 bits

const (
	EmphasisNone Emphasis = iota
	Emphasis50_15
	EmphasisReserved
	EmphasisCCITT_J_17
)

type BitrateIndex uint8 // 4 bits

// BitrateIndexFree indicates the "free format" condition, in which a fixed
// bitrate which does not need to be in the list can be used
const BitrateIndexFree BitrateIndex = 0

func (i BitrateIndex) Bitrate(version MPEGVersion, layer MPEGLayer) (int, bool) {
	if i < 0b1111 {
		switch version {
		case MPEGVersion1:
			switch layer {
			case MPEGLayerI:
				return [0b1111]int{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448}[i], true
			case MPEGLayerII:
				return [0b1111]int{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384}[i], true
			case MPEGLayerIII:
				return [0b1111]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}[i], true
			}
		case MPEGVersion2, MPEGVersion2_5:
			switch layer {
			case MPEGLayerI:
				return [0b1111]int{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256}[i], true
			case MPEGLayerII:
				return [0b1111]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}[i], true
			case MPEGLayerIII:
				return [0b1111]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}[i], true
			}
		}
	}
	return -1, false
}

type SamplingFrequencyIndex uint8 // 2 bits

func (i SamplingFrequencyIndex) SamplingFrequency(version MPEGVersion) (int, bool) {
	if i < 0b11 {
		switch version {
		case MPEGVersion1:
			return [...]int{44100, 48000, 32000}[i], true
		case MPEGVersion2:
			return [...]int{22050, 24000, 16000}[i], true
		case MPEGVersion2_5:
			return [...]int{11025, 12000, 8000}[i], true
		}
	}
	return -1, false
}

// SampleCount is the number of samples a frame contains information for.
//
// In [MPEGLayerI] and [MPEGLayerII], each frame is standalone. In
// [MPEGLayerIII], a frame may depend on information from previous frames.
func SampleCount(version MPEGVersion, layer MPEGLayer) (int, bool) {
	switch version {
	case MPEGVersion1:
		switch layer {
		case MPEGLayerI:
			return 384, true
		case MPEGLayerII:
			return 1152, true
		case MPEGLayerIII:
			return 1152, true
		}
	case MPEGVersion2, MPEGVersion2_5:
		switch layer {
		case MPEGLayerI:
			return 384, true
		case MPEGLayerII:
			return 1152, true
		case MPEGLayerIII:
			return 576, true
		}
	}
	return -1, false
}

func SlotSize(version MPEGVersion, layer MPEGLayer) (int, bool) {
	switch version {
	case MPEGVersion1, MPEGVersion2, MPEGVersion2_5:
		switch layer {
		case MPEGLayerI:
			return 4, true
		case MPEGLayerII, MPEGLayerIII:
			return 1, true
		}
	}
	return -1, false
}

type FrameHeader struct {
	// ID indicates the ID of the algorithm.
	ID MPEGVersion
	// Layer indicates which layer was used.
	Layer MPEGLayer
	// Protection indicates whether redundancy has been added in the audio
	// bitstream to facilitate error detection and concealment.
	Protection bool
	// BitrateIndex indicates the bitrate. It is irrespective of the mode.
	BitrateIndex BitrateIndex
	// SamplingFrequencyIndex indicates the sampling frequency.
	SamplingFrequencyIndex SamplingFrequencyIndex
	// Padding indicates whether the frame contains an additional slot to adjust
	// the mean bitrate to the sampling frequency. Padding is necessary with a
	// sampling frequency of 44.1 kHz. Padding may also be required in free
	// format.
	Padding bool
	// Private is for private use.
	Private bool
	// Mode indicates the mode. In Layer I and II the joint_stereo mode is
	// intensity_stereo, in Layer III it is intensity_stereo and/or ms_stereo.
	// In Layer I and II, in all modes except joint_stereo, the value of bound
	// equals sblimit. In joint_stereo mode the bound is determined by the
	// mode_extension.
	Mode Mode
	// ModeExtension is used when [Mode] is [ModeJointStereo].
	ModeExtension ModeExtension
	// Copyright indicates if there is copyright on the MPEG/Audio bitstream.
	Copyright bool
	// Original indicates if the bitstream is the original rather than a copy.
	Original bool
	// Emphasis indicates the type of de-emphasis that shall be used.
	Emphasis Emphasis
}

// FrameHeaderSize is the length of an encoded [FrameHeader] in bytes.
const FrameHeaderSize = 4

func (f FrameHeader) Bitrate() (int, bool) {
	return f.BitrateIndex.Bitrate(f.ID, f.Layer)
}

func (f FrameHeader) SamplingFrequency() (int, bool) {
	return f.SamplingFrequencyIndex.SamplingFrequency(f.ID)
}

func (f FrameHeader) SampleCount() (int, bool) {
	return SampleCount(f.ID, f.Layer)
}

func (f FrameHeader) SlotSize() (int, bool) {
	return SlotSize(f.ID, f.Layer)
}

// Slots gets the number of slots used for the frame and whether the result was
// truncated. If the result was truncated, the number of slots between syncwords
// will vary between N and N+1.
//
// This includes the entire frame: header, crc, data, and padding.
//
// If bitrate is the free bitrate, -1 is returned, and N must be determined by
// looking at the distance until the next syncword and the value of the padding
// bit.
func (f FrameHeader) Slots() (int, bool, bool) {
	if bitrate, ok := f.Bitrate(); ok { // kbit/s
		if bitrate == int(BitrateIndexFree) {
			return -1, false, false
		}
		if samplingFrequency, ok := f.SamplingFrequency(); ok { // Hz
			bitrate *= 1000 // bit/s
			switch f.Layer {
			case MPEGLayerI:
				x := 12 * bitrate
				return x / samplingFrequency, x%samplingFrequency != 0, true
			case MPEGLayerII, MPEGLayerIII:
				x := 144 * bitrate
				return x / samplingFrequency, x%samplingFrequency != 0, true
			}
		}
	}
	return 0, false, false
}

func (f FrameHeader) Valid() error {
	switch f.ID {
	case MPEGVersion1, MPEGVersion2, MPEGVersion2_5:
	default:
		return errors.New("invalid mpeg version")
	}
	switch f.Layer {
	case MPEGLayerI, MPEGLayerII, MPEGLayerIII:
	default:
		return errors.New("invalid mpeg layer")
	}
	if _, ok := f.BitrateIndex.Bitrate(f.ID, f.Layer); !ok {
		return errors.New("invalid bitrate index")
	}
	if _, ok := f.SamplingFrequencyIndex.SamplingFrequency(f.ID); !ok {
		return errors.New("invalid sampling frequency index")
	}
	switch f.Mode {
	case ModeStereo, ModeDualChannel, ModeSingleChannel:
		// note: we don't need to validate the ModeExtension; it's redundant if it's not 0, but it's not an error
	case ModeJointStereo:
	default:
		return errors.New("invalid mode")
	}
	switch f.Emphasis {
	case EmphasisNone, Emphasis50_15, EmphasisCCITT_J_17:
	default:
		return errors.New("invalid emphasis")
	}
	return nil
}

func (f *FrameHeader) decode(b []byte) {
	_ = b[FrameHeaderSize-1] // size hint
	*f = FrameHeader{
		ID:                     MPEGVersion((b[1] & 0b0001_1000) >> 3),
		Layer:                  MPEGLayer((b[1] & 0b0000_0110) >> 1),
		Protection:             !bitBool((b[1] & 0b0000_0001) >> 0),
		BitrateIndex:           BitrateIndex((b[2] & 0b1111_0000) >> 4),
		SamplingFrequencyIndex: SamplingFrequencyIndex((b[2] & 0b0000_1100) >> 2),
		Padding:                bitBool((b[2] & 0b0000_0010) >> 1),
		Private:                bitBool((b[2] & 0b0000_0001) >> 0),
		Mode:                   Mode((b[3] & 0b1100_0000) >> 6),
		ModeExtension:          ModeExtension((b[3] & 0b0011_0000) >> 4),
		Copyright:              bitBool((b[3] & 0b0000_1000) >> 3),
		Original:               bitBool((b[3] & 0b0000_0100) >> 2),
		Emphasis:               Emphasis((b[3] & 0b0000_0011) >> 0),
	}
}

func bitBool(b uint8) bool {
	return b != 0
}

func (f FrameHeader) encode(b []byte) {
	_ = b[FrameHeaderSize-1] // size hint
	b[0] = 0b1111_1111
	b[1] = 0b1110_0000 |
		uint8(f.ID&0b11)<<3 |
		uint8(f.Layer&0b11)<<1 |
		boolBit(!f.Protection)<<0
	b[2] = 0 |
		uint8(f.BitrateIndex&0b1111)<<4 |
		uint8(f.SamplingFrequencyIndex&0b11)<<2 |
		boolBit(f.Padding)<<1 |
		boolBit(f.Private)<<0
	b[3] = 0 |
		uint8(f.Mode&0b11)<<6 |
		uint8(f.ModeExtension&0b11)<<4 |
		boolBit(f.Copyright)<<3 |
		boolBit(f.Original)<<2 |
		uint8(f.Emphasis&0b11)<<0
}

func boolBit(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

func (f *FrameHeader) ReadFrom(r io.Reader) (n int64, err error) {
	b := make([]byte, FrameHeaderSize)
	nn, err := io.ReadFull(r, b)
	if err != nil {
		return int64(nn), err
	}
	if !IsSyncword(b) {
		return int64(nn), ErrUnsynchronized
	}
	f.decode(b)
	return int64(nn), nil
}

func (f *FrameHeader) UnmarshalBinary(b []byte) error {
	if len(b) != FrameHeaderSize {
		return errors.New("incorrect frame header size " + strconv.Itoa(len(b)))
	}
	if !IsSyncword(b) {
		return ErrUnsynchronized
	}
	f.decode([]byte(b))
	return nil
}

func (f FrameHeader) WriteTo(w io.Writer) (n int64, err error) {
	b := make([]byte, FrameHeaderSize)
	f.encode(b)
	nn, err := w.Write(b)
	return int64(nn), err
}

func (f FrameHeader) MarshalBinary() ([]byte, error) {
	b := make([]byte, FrameHeaderSize)
	f.encode(b)
	return b, nil
}

func (f FrameHeader) AppendBinary(b []byte) ([]byte, error) {
	b = slices.Grow(b, FrameHeaderSize)
	f.encode(b[len(b) : len(b)+FrameHeaderSize])
	b = b[:len(b)+FrameHeaderSize]
	return b, nil
}

// Sync attempts to find the index of the first syncword. If none is found, -1
// is returned.
func Sync(b []byte) int {
	for i := range b {
		if IsSyncword(b[i:]) {
			return i
		}
	}
	return -1
}

func IsSyncword(b []byte) bool {
	return len(b) >= 2 && b[0] == 0b1111_1111 && b[1]&0b1110_0000 == 0b1110_0000
}

// TODO: func ComputeErrorCheck(f Frame, ...) uint16

func (x MPEGVersion) String() string {
	switch x {
	case MPEGVersion1:
		return "mpeg-1"
	case MPEGVersion2:
		return "mpeg-2"
	case MPEGVersion2_5:
		return "mpeg-2.5"
	default:
		return strconv.Itoa(int(x))
	}
}

func (x MPEGLayer) String() string {
	switch x {
	case MPEGLayerIII:
		return "layer-3"
	case MPEGLayerII:
		return "layer-2"
	case MPEGLayerI:
		return "layer-1"
	default:
		return strconv.Itoa(int(x))
	}
}

func (x Mode) String() string {
	switch x {
	case ModeStereo:
		return "stereo"
	case ModeJointStereo:
		return "joint-stereo"
	case ModeDualChannel:
		return "dual-channel"
	case ModeSingleChannel:
		return "single-channel"
	default:
		return strconv.Itoa(int(x))
	}
}

func (x Emphasis) String() string {
	switch x {
	case EmphasisNone:
		return "none"
	case Emphasis50_15:
		return "50/15-microsecond"
	case EmphasisCCITT_J_17:
		return "ccitt-j.17"
	default:
		return strconv.Itoa(int(x))
	}
}

func (f FrameHeader) String() string {
	var b strings.Builder
	b.WriteString("frame(")
	if slotSize, ok := f.SlotSize(); ok {
		if slots, slotsTruncated, ok := f.Slots(); ok {
			b.WriteString(strconv.Itoa(slots))
			b.WriteByte('*')
			b.WriteString(strconv.Itoa(slotSize))
			b.WriteByte('=')
			b.WriteString(strconv.Itoa(slots * slotSize))
			if slotsTruncated {
				b.WriteByte('+')
			}
		}
	}
	b.WriteString("){")
	b.WriteString(f.ID.String())
	b.WriteByte(',')
	b.WriteString(f.Layer.String())
	b.WriteByte(',')
	b.WriteString(f.Mode.String())
	if f.Mode == ModeJointStereo {
		b.WriteString(",(")
		switch f.Layer {
		case MPEGLayerI, MPEGLayerII:
			if bound, ok := f.ModeExtension.Bound(); ok {
				b.WriteString("bound=")
				b.WriteString(strconv.Itoa(bound))
			}
		case MPEGLayerIII:
			if intensityStereo, msStereo, ok := f.ModeExtension.Coding(); ok {
				if intensityStereo {
					b.WriteString("intensity-stereo")
				}
				if intensityStereo && msStereo {
					b.WriteByte('+')
				}
				if intensityStereo {
					b.WriteString("ms-stereo")
				}
			}
		default:
			b.WriteString("?")
		}
		if f.Layer == MPEGLayerIII {

		}
		b.WriteString(")")
	}
	if freq, ok := f.SamplingFrequency(); ok {
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(freq))
		b.WriteString("-Hz")
	}
	if bitrate, ok := f.Bitrate(); ok {
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(bitrate))
		b.WriteString("-kbit/s")
	}
	if f.Protection {
		b.WriteByte(',')
		b.WriteString("crc")
	}
	if f.Private {
		b.WriteByte(',')
		b.WriteString("private")
	}
	if f.Copyright {
		b.WriteByte(',')
		b.WriteString("copyright")
	}
	if f.Original {
		b.WriteByte(',')
		b.WriteString("original")
	}
	b.WriteString("}")
	return b.String()
}
