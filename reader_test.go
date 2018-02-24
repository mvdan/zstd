package zstd

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReader(t *testing.T) {
	names, _ := filepath.Glob(filepath.Join("testdata", "*.zst"))
	for _, name := range names {
		decName := strings.TrimSuffix(name, ".zst")
		t.Run(decName, func(t *testing.T) {
			testRead(t, decName)
		})
	}
}

func testRead(t *testing.T, decName string) {
	want, err := ioutil.ReadFile(decName)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(decName + ".zst")
	if err != nil {
		t.Fatal(err)
	}

	r := NewReader(f)
	got, err := ioutil.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(want) {
		t.Fatalf("got:  %q\nwant: %q", string(got), string(want))
	}
}
