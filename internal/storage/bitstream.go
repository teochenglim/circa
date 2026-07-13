package storage

// bitWriter accumulates bits MSB-first into a growable byte slice — the
// building block for Gorilla-style variable-width encoding, where most
// fields (a repeated timestamp delta, an all-zero XOR) cost far fewer than
// 8 bits.
type bitWriter struct {
	buf  []byte
	bitN uint8 // number of valid bits already written in buf's last byte
}

func (w *bitWriter) writeBit(bit bool) {
	if w.bitN == 0 {
		w.buf = append(w.buf, 0)
	}
	if bit {
		w.buf[len(w.buf)-1] |= 1 << (7 - w.bitN)
	}
	w.bitN++
	if w.bitN == 8 {
		w.bitN = 0
	}
}

// writeBits writes the low nBits bits of v, most-significant first.
func (w *bitWriter) writeBits(v uint64, nBits int) {
	for i := nBits - 1; i >= 0; i-- {
		w.writeBit((v>>uint(i))&1 == 1)
	}
}

func (w *bitWriter) bytes() []byte {
	return w.buf
}

// bitReader reads bits MSB-first out of a byte slice written by bitWriter.
type bitReader struct {
	buf     []byte
	bytePos int
	bitPos  uint8 // 0-7, next bit to read within buf[bytePos]
	overrun bool  // set once a read goes past the end of buf — signals truncated/corrupt input
}

func newBitReader(buf []byte) *bitReader {
	return &bitReader{buf: buf}
}

func (r *bitReader) readBit() bool {
	if r.bytePos >= len(r.buf) {
		r.overrun = true
		return false
	}
	bit := (r.buf[r.bytePos]>>(7-r.bitPos))&1 == 1
	r.bitPos++
	if r.bitPos == 8 {
		r.bitPos = 0
		r.bytePos++
	}
	return bit
}

func (r *bitReader) readBits(nBits int) uint64 {
	var v uint64
	for i := 0; i < nBits; i++ {
		v <<= 1
		if r.readBit() {
			v |= 1
		}
	}
	return v
}

// exhausted reports whether every byte in the underlying buffer has been consumed.
func (r *bitReader) exhausted() bool {
	return r.bytePos >= len(r.buf)
}
