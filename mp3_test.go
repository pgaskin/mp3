package mp3

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"
)

//go:embed testdata
var testdata embed.FS

var (
	VerboseFrame = flag.Bool("mp3.vframe", false, "log frame info in some tests")
)

func TestRoundtrip(t *testing.T) {
	t.Parallel()
	if err := fs.WalkDir(testdata, "testdata", func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() {
			return nil
		}
		switch path.Ext(p) {
		case ".mpg", ".mp1", ".mp2", ".mp3":
			buf, err := fs.ReadFile(testdata, p)
			if err != nil {
				return err
			}
			t.Run(p[len("testdata/"):len(p)-len(".xxx")], func(t *testing.T) {
				testRoundtrip(t, buf)
			})
		}
		return nil
	}); err != nil {
		panic(err)
	}
}

func testRoundtrip(t *testing.T, buf []byte) {
	if strings.HasSuffix(t.Name(), "/layer3/he_free") {
		t.SkipNow() // not implemented yet
	}
	r := NewReader(bytes.NewReader(buf), 16384)
	n := 0         // frame number
	o := Sync(buf) // expected offset
	ts := time.Duration(0)
	for r.Next() {
		n++
		if *VerboseFrame {
			t.Logf("read [% 4d] % 6d + % 4d (%.3fs) :: %s\n", n, r.Offset()-int64(len(r.Raw())), len(r.Raw()), ts.Seconds(), r.Header())
		}
		ts = r.Time()

		o += len(r.Raw())
		if o != int(r.Offset()) {
			t.Errorf("frame %d: frames are not back-to-back after first syncword", n)
		}

		buf, _ := r.Header().AppendBinary(nil)
		buf = append(buf, r.Raw()[FrameHeaderSize:]...)
		if !bytes.Equal(r.Raw(), buf) {
			t.Errorf("frame %d: re-encoded frame differs", n)
		}
	}
	if n == 0 {
		t.Errorf("no frames read?!?")
	} else {
		t.Logf("read %d frames (%s)", n, ts)
	}
	err := r.Err()
	if err != nil {
		err = fmt.Errorf("frame %d (offset %d): %w", n+1, r.Offset(), err)
	}
	switch {
	case strings.HasSuffix(t.Name(), "/layer3/compl"):
		if exp := "frame 217 (offset 41472): unexpected EOF"; err == nil || err.Error() != exp {
			t.Errorf("expected error %v, got %v", exp, err)
		}
		return
	case strings.HasSuffix(t.Name(), "/layer3/sin1k0db"): // TODO: is this error expected?
		if exp := "frame 318 (offset 132708): unexpected EOF"; err == nil || err.Error() != exp {
			t.Errorf("expected error %v, got %v", exp, err)
		}
		return
	case strings.HasSuffix(t.Name(), "/mpeg2/test23"):
		if exp := "frame 342 (offset 327360): unexpected EOF"; err == nil || err.Error() != exp {
			t.Errorf("expected error %v, got %v", exp, err)
		}
		return
	}
	if err != nil {
		t.Errorf("read frames: %v", err)
	}

	// this one is an interesting VBR one, so it's a good test case for this
	if strings.HasSuffix(t.Name(), "/layer3/he_48khz") {
		if act, exp := r.Time().Truncate(time.Millisecond).String(), "3.6s"; act != exp {
			t.Errorf("expected duration %s, got %s", act, exp)
		}
	}

	// TODO: test writing back
}
