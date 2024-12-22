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
	n := 0
	for r.Next() {
		n++
		if *VerboseFrame {
			t.Logf("read [% 4d] % 6d + % 4d :: %s\n", n, r.Offset(), len(r.Raw()), r.Header())
		}
	}
	if n == 0 {
		t.Errorf("no frames read?!?")
	} else {
		t.Logf("read %d frames", n)
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
	// TODO: test writing back
}
